// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
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

// fakeFFmpeg writes fixed bytes to stdout and ignores every argument, which is
// all this needs: what is under test is the *response shape* the origin puts
// around a remux, not what ffmpeg produces.
func fakeFFmpeg(t *testing.T, output string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nprintf '%s' " + strconv.Quote(output) + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return bin
}

// TestRemuxedResponseIsNotSeekable pins the asymmetry M3 slice 4 exists to
// remove, and it is pinned because the slice is *scoped* against it.
//
// The origin has two paths and only one of them is a pipe. The relayed path
// forwards Range upstream and relays the 206 back, which
// TestHandlerRelaysRangeRequests covers. This is the other one: fragmented MP4
// off a pipe has no index and no length, so a Range request cannot be honoured
// and the honest answer is to say the source does not do them.
//
// Asserting it matters more than it looks. Until now the difference between the
// two paths was true only in the code and in a comment — a reading, not a test —
// and the roadmap sentence built on it ("resume is exact only on a directly
// relayed stream") was being read as a property of the *source* rather than of
// this implementation. When the segmenter lands, this test is what has to change
// to say so, rather than the change happening quietly.
func TestRemuxedResponseIsNotSeekable(t *testing.T) {
	const output = "fragmented-mp4-bytes"

	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1",
		Plan{Reason: "matroska cannot pass through MSE"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	h := Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, output)))

	// A Range request, because that is what a player sends the moment someone
	// drags the scrubber. It must not be answered with a 206 over bytes that
	// were never indexed.
	req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
	req.Header.Set("Range", "bytes=8-15")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d — a remuxed stream has no index to range over", rec.Code, http.StatusOK)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "none" {
		t.Errorf("Accept-Ranges = %q, want %q — anything else promises seeking this path cannot do", got, "none")
	}
	if got := rec.Header().Get("Content-Range"); got != "" {
		t.Errorf("Content-Range = %q, want it absent", got)
	}
	// The whole output, not the requested slice: the range was not honoured, and
	// the response says so rather than quietly returning the wrong bytes.
	if got := rec.Body.String(); got != output {
		t.Errorf("body = %q, want the whole stream %q", got, output)
	}
	if got := rec.Header().Get("Content-Type"); got != "video/mp4" {
		t.Errorf("Content-Type = %q, want video/mp4", got)
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
