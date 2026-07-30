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
	"time"
)

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

// TestRemuxedResponseIsNotSeekable now pins the **fallback**, which is what this
// test became when the seekable path landed.
//
// A plan with no Duration cannot map a byte offset to a timestamp, so there is
// nothing to restart ffmpeg at and the origin serves the old honest pipe:
// Accept-Ranges: none, 200, the whole stream. That case is real — a source that
// reports no duration — and saying so is better than synthesising a timeline and
// sending a player to a position that does not exist.
//
// It previously pinned the *only* remux behaviour, and its comment said this
// test is what would have to change when the segmenter landed rather than the
// behaviour changing quietly. That is what happened.
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

// TestARemoteUpstreamIsReconnectedTo covers the failure that ends a long play
// silently: the remux path holds one HTTP connection for the length of a film,
// and a debrid CDN closing it partway through is ordinary. Without these flags
// ffmpeg stops, readers see a clean EOF, and the stream ends mid-scene with
// nothing reporting an error.
//
// The flags must precede -i, because they configure the input protocol.
func TestARemoteUpstreamIsReconnectedTo(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{Reason: "eac3"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(recordingFFmpeg(t, "frag", argsFile))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ffmpeg was never started: %v", err)
	}
	line := string(got)
	for _, want := range []string{"-reconnect 1", "-reconnect_streamed 1", "-reconnect_delay_max 5"} {
		if !strings.Contains(line, want) {
			t.Errorf("ffmpeg args %q lack %q — a dropped upstream ends the film instead of resuming", line, want)
		}
	}
	// Deliberately absent: it treats a legitimate end-of-file as an error worth
	// retrying, which is right for a live stream and turns the last frame of a
	// finite release into a reconnect loop.
	if strings.Contains(line, "-reconnect_at_eof") {
		t.Errorf("ffmpeg args %q carry -reconnect_at_eof; these inputs are finite and it would retry a real end of stream", line)
	}
	if strings.Index(line, "-reconnect") > strings.Index(line, "-i ") {
		t.Errorf("ffmpeg args %q put the reconnect flags after -i, where they configure nothing", line)
	}
}

// TestANonHTTPUpstreamCarriesNoReconnectFlags is the guard, and it is not
// tidiness: -reconnect is an option of the HTTP protocol, and ffmpeg exits with
// "Option reconnect not found" when nothing consumes one. Passing it
// unconditionally would make every non-HTTP resolution unplayable in the name of
// making HTTP ones more reliable.
func TestANonHTTPUpstreamCarriesNoReconnectFlags(t *testing.T) {
	for _, url := range []string{"file:///srv/media/movie.mkv", "rtsp://box.local/live", "srt://host:9000"} {
		if got := reconnectArgs(url); got != nil {
			t.Errorf("reconnectArgs(%q) = %v, want none — ffmpeg exits when the protocol cannot consume them", url, got)
		}
	}
	// Case-insensitively, because a scheme is not case-sensitive and a module is
	// free to hand back the one it was given.
	if got := reconnectArgs("HTTPS://cdn.example/movie.mkv"); len(got) == 0 {
		t.Error("reconnectArgs did not recognise an upper-case https scheme")
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

// TestRemuxFailureIsNotASuccessfulEmptyStream pins the honest failure, and it is
// pinned because the dishonest one shipped.
//
// serveRemuxed used to write 200 before reading a byte, so an ffmpeg that died
// on its own arguments produced a successful response with an empty body. The
// player reported only "format not supported", the access log said status=200,
// and ffmpeg's stderr — the one place that knew — was the one place nobody
// looked. A FLAC track the MP4 muxer refuses presented as a broken file.
func TestRemuxFailureIsNotASuccessfulEmptyStream(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	// A fake that fails exactly as ffmpeg does: a diagnostic on stderr, nothing
	// on stdout, non-zero exit.
	bin := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\necho 'flac in MP4 support is experimental' >&2\nexit 1\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}

	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1",
		Plan{Reason: "HDR10 needs tone-mapping for this client"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(bin)).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	if rec.Code == http.StatusOK {
		t.Fatalf("status = 200 with %d bytes — a failed remux must not read as success",
			rec.Body.Len())
	}
	if rec.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadGateway)
	}
	// The plan's reason travels with the failure: "playback failed" sends
	// somebody to the wrong place, and this is the only signal a user gets.
	if body := rec.Body.String(); !strings.Contains(body, "tone-mapping") {
		t.Errorf("body %q does not carry the reason the remux was attempted", body)
	}
}

// The success path still streams, so the probe-before-status change did not turn
// a working remux into a buffered one or lose the leading bytes.
func TestRemuxSuccessStillStreamsFromTheFirstByte(t *testing.T) {
	const output = "fragmented-mp4-bytes"

	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, output))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Body.String(); got != output {
		t.Errorf("body = %q, want %q — the probed bytes must be written through, not dropped", got, output)
	}
}

// recordingFFmpeg writes bytes to stdout and records the arguments it was given,
// so a test can assert on what ffmpeg was actually asked to do. The seek is
// invisible in the response — every seek returns the same kind of fMP4 — so the
// argument list is the only place it can be observed.
func recordingFFmpeg(t *testing.T, output, argsFile string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	bin := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\necho \"$@\" > " + strconv.Quote(argsFile) + "\nprintf '%s' " + strconv.Quote(output) + "\nsleep 5\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}
	return bin
}

// seekablePlan is a two-hour release, which is the only thing that makes the
// remux path answer ranges at all.
func seekablePlan() Plan {
	return Plan{Duration: 2 * time.Hour, Reason: "audio codec eac3 is not decodable by this client"}
}

// readEventually waits for the fake ffmpeg to have written its argument list.
// The process is started and not waited on — Segment returns as soon as it is
// running, deliberately, because a transcode outlives the request that began it.
func readEventually(t *testing.T, path string) string {
	t.Helper()
	for range 100 {
		if b, err := os.ReadFile(path); err == nil && len(b) > 0 {
			return string(b)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("ffmpeg never wrote %s", path)
	return ""
}
