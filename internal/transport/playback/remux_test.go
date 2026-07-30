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
	Handler(s, http.DefaultClient, NewRemuxerAt(recordingFFmpeg(t, "frag", args))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodHead, "/playback/"+raw, nil))

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
	Handler(s, http.DefaultClient, NewRemuxerAt(recordingFFmpeg(t, "frag", argsFile))).ServeHTTP(rec, req)

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
	Handler(s, http.DefaultClient, NewRemuxerAt(recordingFFmpeg(t, "frag", argsFile))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

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
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "frag"))).ServeHTTP(rec, req)

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

// TestOverlappingRangesComeFromOneTranscode is the test the live failure earned.
//
// The design this replaces answered each range with its own ffmpeg started at
// the mapped timestamp. Every response was correct in isolation and six unit
// tests passed, but a media element issues *overlapping* ranges, so the browser
// concatenated bytes from two transcodes at different timestamps into what it
// believed was one file — disjoint buffered slivers and MEDIA_ERR_DECODE. Only a
// real decoder could show it, which is why this asserts the property directly:
// count the processes, and compare the bytes.
func TestOverlappingRangesComeFromOneTranscode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	dir := t.TempDir()
	countFile := filepath.Join(dir, "starts")
	bin := filepath.Join(dir, "ffmpeg")
	// Appends a line per launch and emits a recognisable, position-dependent
	// stream, so a second transcode is visible both in the count and the bytes.
	script := "#!/bin/sh\necho start >> " + strconv.Quote(countFile) +
		"\nprintf 'ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}

	plan := seekablePlan()
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	h := Handler(s, http.DefaultClient, NewRemuxerAt(bin))

	// Three overlapping reads, the shape a media element actually produces.
	get := func(rangeHeader string) string {
		req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Body.String()
	}
	whole := get("")
	fromFour := get("bytes=4-")
	alsoFromFour := get("bytes=4-")

	starts, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("ffmpeg never started: %v", err)
	}
	if n := strings.Count(string(starts), "start"); n != 1 {
		t.Errorf("ffmpeg started %d times for overlapping ranges of one playback; want 1 — this is the defect exactly", n)
	}

	// The same offset must yield the same bytes. Two transcodes would not
	// guarantee that even when both are individually valid.
	if fromFour != alsoFromFour {
		t.Errorf("two reads of the same range disagree:\n %q\n %q", fromFour, alsoFromFour)
	}
	// And a ranged read must be the tail of the unranged one, which is what
	// "one coherent byte stream" means.
	if !strings.HasSuffix(whole, fromFour) {
		t.Errorf("the range at 4 is not a tail of the whole stream:\n whole %q\n at4   %q", whole, fromFour)
	}
}

// A genuine seek — far past what the transcode has produced — does restart, and
// must, or the viewer waits for the encoder to reach the hour mark in real time.
// That restart is affordable only because the upstream honours Range.
func TestASeekBeyondTheFrontierRestarts(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	dir := t.TempDir()
	countFile := filepath.Join(dir, "starts")
	bin := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\necho start >> " + strconv.Quote(countFile) + "\nprintf 'frag'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}

	plan := seekablePlan()
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	h := Handler(s, http.DefaultClient, NewRemuxerAt(bin))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
	req.Header.Set("Range", "bytes="+strconv.FormatInt(plan.contentLength()/2, 10)+"-")
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec2, req)

	if rec2.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want 206", rec2.Code)
	}
	starts, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("ffmpeg never started: %v", err)
	}
	if n := strings.Count(string(starts), "start"); n != 2 {
		t.Errorf("ffmpeg started %d times; want 2 — a seek to the middle must restart rather than wait for the encoder to arrive", n)
	}
}

// TestAdvertisedLengthTracksTheSource pins the agreement a seek depends on.
//
// The client computes byte-to-time from the length the origin advertises and
// the duration it infers from the fragments; the origin computes the inverse
// from the same length and the probed duration. If those disagree, a seek
// resolves to a timestamp nobody asked for — live, a flat 2 MB/s guess against
// a 4K release whose real rate was 45 MB/s put the two mappings a factor of
// twenty-two apart.
func TestAdvertisedLengthTracksTheSource(t *testing.T) {
	const size = 21_474_836_480
	withSource := Plan{Duration: 2 * time.Hour, SourceBytes: size}
	if got := withSource.contentLength(); got != size {
		t.Errorf("contentLength = %d, want the source's own %d — a guess disagrees with what the client infers", got, size)
	}

	// The flat rate survives only for a source that reported no size, where
	// there is nothing better to say.
	noSource := Plan{Duration: 100 * time.Second}
	if got := noSource.contentLength(); got != 100*estimatedBitrate {
		t.Errorf("contentLength = %d, want the fallback estimate", got)
	}

	// And the mapping stays proportional over whichever total is in use, since
	// that is what the origin inverts on every range request.
	if got := withSource.offsetAt(size / 4); got != 30*time.Minute {
		t.Errorf("offsetAt(quarter) = %v, want 30m", got)
	}
}

// TestReadingTwoRegionsDoesNotDestroyEither is the second live failure written
// down as a test.
//
// A media element reads more than one region of a file at once — the head for
// the header, and for a progressive MP4 the tail, where a moov would normally
// live. Keeping one session per playback and killing it whenever a request fell
// outside it meant the client's second region destroyed the first and the first
// destroyed the second: one playback wrote 7.4 GB across two spools and the
// player never reached readyState 1, because nothing it asked for survived long
// enough to be read.
func TestReadingTwoRegionsDoesNotDestroyEither(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the fake is a shell script")
	}
	dir := t.TempDir()
	countFile := filepath.Join(dir, "starts")
	bin := filepath.Join(dir, "ffmpeg")
	script := "#!/bin/sh\necho start >> " + strconv.Quote(countFile) + "\nprintf 'ABCDEFGHIJ'\n"
	if err := os.WriteFile(bin, []byte(script), 0o700); err != nil {
		t.Fatalf("writing the fake ffmpeg: %v", err)
	}

	plan := seekablePlan()
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	h := Handler(s, http.DefaultClient, NewRemuxerAt(bin))

	get := func(rangeHeader string) (int, string) {
		req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
		if rangeHeader != "" {
			req.Header.Set("Range", rangeHeader)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code, rec.Body.String()
	}

	total := plan.contentLength()
	// The head, then a region far away, then the head again — the interleaving a
	// browser actually performs.
	_, head1 := get("bytes=0-")
	if code, _ := get("bytes=" + strconv.FormatInt(total/2, 10) + "-"); code != http.StatusPartialContent {
		t.Fatalf("far region status = %d, want 206", code)
	}
	_, head2 := get("bytes=0-")

	if head1 != head2 {
		t.Errorf("the head changed after another region was read:\n first  %q\n second %q", head1, head2)
	}
	// Two regions, two transcodes — and the head must not have been restarted a
	// third time, which is what the destroy-and-rebuild loop did.
	starts, err := os.ReadFile(countFile)
	if err != nil {
		t.Fatalf("ffmpeg never started: %v", err)
	}
	if n := strings.Count(string(starts), "start"); n != 2 {
		t.Errorf("ffmpeg started %d times for two regions read twice; want 2 — a third means the sessions are evicting each other", n)
	}
}
