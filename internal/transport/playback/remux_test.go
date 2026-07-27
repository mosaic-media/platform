// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"fmt"
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
	script := "#!/bin/sh\necho \"$@\" > " + strconv.Quote(argsFile) + "\nprintf '%s' " + strconv.Quote(output) + "\n"
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

// The seekable component's tests call it directly, because the Handler does not
// dispatch to it: restarting ffmpeg per range produced a stream the browser
// could not decode, and the path is unwired until it is served from a single
// file (see serveRemuxed). They are kept and kept passing because the mapping,
// the flags and the range arithmetic are all reused by that design — what was
// wrong was where the bytes came from, not how the offset was computed.

// TestSeekableRemuxAdvertisesALength is what a media element checks before it
// will let anyone drag the scrubber. Without a length and an Accept-Ranges it
// treats the source as an unbounded stream and disables seeking entirely, which
// is exactly the state slice 4 exists to leave.
//
// The HEAD must not start a transcode: it is asked once at load, and answering
// it by spawning ffmpeg would burn a process per page view.
func TestSeekableRemuxAdvertisesALength(t *testing.T) {
	args := filepath.Join(t.TempDir(), "args")
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", seekablePlan())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	serveSeekableRemux(rec, httptest.NewRequest(http.MethodHead, "/playback/"+raw, nil),
		NewRemuxerAt(recordingFFmpeg(t, "frag", args)), ticket{URL: "https://cdn.example/movie.mkv", Plan: seekablePlan()}, seekablePlan())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Accept-Ranges"); got != "bytes" {
		t.Errorf("Accept-Ranges = %q, want bytes — anything else disables the scrubber", got)
	}
	want := strconv.FormatInt(seekablePlan().contentLength(), 10)
	if got := rec.Header().Get("Content-Length"); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if _, err := os.Stat(args); err == nil {
		t.Error("a HEAD started ffmpeg; it must answer from the plan alone")
	}
}

// TestSeekableRemuxRestartsFFmpegAtTheMappedTime is the mechanism itself.
//
// A byte offset means nothing to a live transcode, so it is converted to a
// position in the release and ffmpeg is restarted there. Both flags are asserted
// because both are load-bearing and neither is visible in the response: `-ss`
// before `-i` makes ffmpeg range the source instead of decoding from zero, and
// `-copyts` keeps the source's timestamps so the player's clock lands at the
// seek point rather than jumping back to 0.
func TestSeekableRemuxRestartsFFmpegAtTheMappedTime(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	plan := seekablePlan()
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	total := plan.contentLength()
	half := total / 2

	req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
	req.Header.Set("Range", "bytes="+strconv.FormatInt(half, 10)+"-")
	rec := httptest.NewRecorder()
	serveSeekableRemux(rec, req, NewRemuxerAt(recordingFFmpeg(t, "frag", argsFile)),
		ticket{URL: "https://cdn.example/movie.mkv", Plan: plan}, plan)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206 — a seek that answers 200 is a seek the player ignores", rec.Code)
	}
	wantRange := fmt.Sprintf("bytes %d-%d/%d", half, total-1, total)
	if got := rec.Header().Get("Content-Range"); got != wantRange {
		t.Errorf("Content-Range = %q, want %q", got, wantRange)
	}

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ffmpeg was never started: %v", err)
	}
	line := string(got)
	// Half the advertised bytes is half the running time: one hour in.
	if !strings.Contains(line, "-ss 3600.000") {
		t.Errorf("ffmpeg args %q do not seek to the mapped time (3600s)", line)
	}
	if !strings.Contains(line, "-copyts") {
		t.Errorf("ffmpeg args %q lack -copyts; without it the player's clock resets to zero on every seek", line)
	}
	// -ss must precede -i, or ffmpeg decodes from the start and discards, which
	// turns a seek into a full read of everything before it.
	if strings.Index(line, "-ss") > strings.Index(line, "-i ") {
		t.Errorf("ffmpeg args %q put -ss after -i, which decodes from zero instead of ranging the source", line)
	}
}

// A play from the start must not carry the seek flags, so the ordinary path is
// exactly what it was before seeking existed.
func TestUnseekedRemuxCarriesNoSeekFlags(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", seekablePlan())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	serveSeekableRemux(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil),
		NewRemuxerAt(recordingFFmpeg(t, "frag", argsFile)),
		ticket{URL: "https://cdn.example/movie.mkv", Plan: seekablePlan()}, seekablePlan())

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an unranged request", rec.Code)
	}
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ffmpeg was never started: %v", err)
	}
	if strings.Contains(string(got), "-ss") || strings.Contains(string(got), "-copyts") {
		t.Errorf("ffmpeg args %q carry seek flags for a play from the start", got)
	}
}

// A range past the advertised end is refused rather than answered from the
// beginning, which would silently ignore the seek and replay the film.
func TestSeekPastTheEndIsRefused(t *testing.T) {
	plan := seekablePlan()
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
	req.Header.Set("Range", "bytes="+strconv.FormatInt(plan.contentLength()+1, 10)+"-")
	rec := httptest.NewRecorder()
	serveSeekableRemux(rec, req, NewRemuxerAt(fakeFFmpeg(t, "frag")),
		ticket{URL: "https://cdn.example/movie.mkv", Plan: plan}, plan)

	if rec.Code != http.StatusRequestedRangeNotSatisfiable {
		t.Errorf("status = %d, want 416", rec.Code)
	}
}

// TestOffsetMappingIsProportionalAndClamped covers the arithmetic on its own,
// including the two ends. Clamping matters: a player that asks past the end must
// get the end rather than a timestamp beyond the film, which ffmpeg answers with
// an empty stream that reads as a broken seek.
func TestOffsetMappingIsProportionalAndClamped(t *testing.T) {
	plan := Plan{Duration: 100 * time.Second}
	total := plan.contentLength()

	cases := []struct {
		at   int64
		want time.Duration
	}{
		{0, 0},
		{total / 4, 25 * time.Second},
		{total / 2, 50 * time.Second},
		{total, 100 * time.Second},
		{total * 2, 100 * time.Second},
		{-1, 0},
	}
	for _, c := range cases {
		if got := plan.offsetAt(c.at); got != c.want {
			t.Errorf("offsetAt(%d) = %v, want %v", c.at, got, c.want)
		}
	}

	// A plan with no duration is not seekable at all, which is what sends it to
	// the pipe rather than to a synthesised timeline.
	if (Plan{}).seekable() {
		t.Error("a plan with no duration reported itself seekable")
	}
}
