// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestShouldRemuxPicksContainersMSECannotTake is the whole decision in one
// place: Matroska and friends cannot pass through Media Source Extensions
// whatever codec is inside, and MP4-family containers can.
func TestShouldRemuxPicksContainersMSECannotTake(t *testing.T) {
	cases := map[string]bool{
		"https://cdn.example/Show.S01E01.1080p.mkv": true,
		"https://cdn.example/movie.avi":             true,
		"https://cdn.example/stream.ts":             true,
		"https://cdn.example/movie.mp4":             false,
		"https://cdn.example/movie.m4v":             false,
		"https://cdn.example/movie.webm":            false,
		"https://cdn.example/movie.mov":             false,
		// A query string must not defeat the extension match — debrid links
		// carry signatures and expiry params on essentially every URL.
		"https://cdn.example/Show.mkv?token=abc&exp=123": true,
		"https://cdn.example/movie.mp4?token=abc":        false,
		// An extensionless URL is relayed rather than guessed at: failing safe
		// here means a playable file is never needlessly piped through ffmpeg.
		"https://cdn.example/dl/8f3a91c2": false,
	}

	for url, want := range cases {
		if got := ShouldRemux(url); got != want {
			t.Errorf("ShouldRemux(%q) = %v, want %v", url, got, want)
		}
	}
}

func TestRemuxerWithoutFFmpegIsUnavailable(t *testing.T) {
	rx := NewRemuxerAt("")
	if rx.Available() {
		t.Fatal("a Remuxer with no binary reported itself available")
	}
	if _, _, err := rx.Stream(t.Context(), "https://cdn.example/a.mkv", nil, Plan{}); err != ErrRemuxUnavailable {
		t.Errorf("Stream error = %v, want ErrRemuxUnavailable", err)
	}
}

// TestRemuxTicketWithoutFFmpegSaysSo pins the honest failure. Without ffmpeg a
// Matroska release cannot play, and the user needs to be told which of the two
// things is missing rather than getting a generic playback error.
func TestRemuxTicketWithoutFFmpegSaysSo(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{Reason: "audio codec eac3 is not decodable by this client"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt("")).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotImplemented)
	}
	if body := rec.Body.String(); !strings.Contains(body, "ffmpeg") {
		t.Errorf("body %q does not name the missing piece", body)
	}
}

// TestFFmpegHeaderArgUsesCRLF guards the form ffmpeg's -headers flag needs: a
// credentialed upstream must be reachable by the remux path on the same terms
// as the relay path, and the delimiter is what makes that work.
func TestFFmpegHeaderArgUsesCRLF(t *testing.T) {
	if got := ffmpegHeaderArg(nil); got != "" {
		t.Errorf("no headers should render empty, got %q", got)
	}
	got := ffmpegHeaderArg(map[string]string{"Authorization": "Bearer abc"})
	if got != "Authorization: Bearer abc\r\n" {
		t.Errorf("header arg = %q, want CRLF-terminated", got)
	}
}
