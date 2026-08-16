// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

// segmentedPlan is a release long enough to divide, which is what makes the
// origin serve HLS rather than the unseekable pipe.
func segmentedPlan() Plan {
	return Plan{Duration: 2 * time.Hour, Reason: "audio codec eac3 is not decodable by this client"}
}

// TestThePlaylistIsServedAtTheTicket is the entry point a client is handed. It
// must answer without starting a transcode: a playlist is derived from the
// probed duration alone, and spawning ffmpeg to render one would burn a process
// per page load.
func TestThePlaylistIsServedAtTheTicket(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", segmentedPlan())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "never-runs"))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/index.m3u8", nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != mediaPlaylistType {
		t.Errorf("Content-Type = %q, want %q — a client that gets this wrong plays the playlist as media", got, mediaPlaylistType)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "#EXT-X-ENDLIST") || !strings.Contains(body, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Error("the playlist is not a closed VOD list, so a player will not seek past what has been produced")
	}
	want := segmentCount(segmentedPlan().Duration, encodedSegmentLength)
	if n := strings.Count(body, ".m4s"); n != want {
		t.Errorf("playlist lists %d segments, want the whole release's %d", n, want)
	}
}

// TestAnUnprobedReleaseStillGetsThePipe is the honest degradation, and it is why
// the pipe path survives the segmenter. Without a duration there is nothing to
// divide, so the origin serves the unseekable stream and says so rather than
// inventing a timeline.
func TestAnUnprobedReleaseStillGetsThePipe(t *testing.T) {
	const output = "fragmented-mp4-bytes"
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{Reason: "eac3"})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, output))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	if rec.Code != http.StatusOK || rec.Header().Get("Accept-Ranges") != "none" {
		t.Errorf("status=%d Accept-Ranges=%q, want 200 and none", rec.Code, rec.Header().Get("Accept-Ranges"))
	}
	if rec.Body.String() != output {
		t.Errorf("body = %q, want the whole stream", rec.Body.String())
	}
}

// TestASegmentPastTheEndIsRefused covers the bound. The playlist closes with
// ENDLIST so a well-behaved player never asks, which makes a request past the
// end a malformed one rather than a race — and answering it would start a
// transcode at a position the release does not have.
func TestASegmentPastTheEndIsRefused(t *testing.T) {
	s := newTestSealer(t)
	plan := segmentedPlan()
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	past := segmentCount(plan.Duration, encodedSegmentLength)

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "x"))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+strconv.Itoa(past)+".m4s", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 for a segment past the end of the release", rec.Code)
	}
}

// TestASegmentNameIsNotAPath pins the safety property. The resource arrives in a
// URL and is matched against a closed set so it never becomes a filename — a
// directory traversal here reads whatever the process can.
func TestASegmentNameIsNotAPath(t *testing.T) {
	for _, resource := range []string{
		"../../etc/passwd", "..%2f..%2fetc%2fpasswd", "0.m4s.bak", "-1.m4s",
		"00.m4s", "1e3.m4s", "ffmpeg.m3u8", ".m4s", "+1.m4s",
	} {
		if n, ok := segmentIndexOf(resource); ok {
			t.Errorf("segmentIndexOf(%q) = %d, accepted — only the exact form the playlist emits may be", resource, n)
		}
	}
	// The forms the playlist really emits must still parse.
	for n := range 3 {
		if got, ok := segmentIndexOf(segmentName(n)); !ok || got != n {
			t.Errorf("segmentIndexOf(%q) = %d,%v — the playlist's own names must parse", segmentName(n), got, ok)
		}
	}
}

// TestAnUnknownResourceIsNotFound guards the closed set. Anything under a ticket
// that is not the playlist, the init segment or a numbered segment is not a
// thing this origin serves.
func TestAnUnknownResourceIsNotFound(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", segmentedPlan())
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	for _, resource := range []string{"ffmpeg.m3u8", "master.m3u8", "0.ts", "anything"} {
		rec := httptest.NewRecorder()
		Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "x"))).
			ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+resource, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%q gave status %d, want 404", resource, rec.Code)
		}
	}
}

// TestARelayedStreamHasNoSubResources keeps the two surfaces apart. A release
// needing no work is the upstream's own bytes at the ticket itself, so a
// playlist under it would be a promise the origin does not keep.
func TestARelayedStreamHasNoSubResources(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mp4", nil, "session-1", Plan{DirectPlay: true})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "x"))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/index.m3u8", nil))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404 — a relayed stream is not segmented", rec.Code)
	}
}

// TestSegmentArgsCarryThePositionAndTheNumbering is the mechanism platform#82 turns
// on, asserted where it is observable: nothing in the response says which
// timestamp a transcode was started at, so the argument list is the only place.
//
// -ss before -i makes ffmpeg range the source rather than decode from zero;
// -copyts makes the segment carry the timestamps its index promises; and
// -start_number names the output for its position in the release rather than in
// this run, which is what lets a restart mid-film write the segment the client
// asked for.
func TestSegmentArgsCarryThePositionAndTheNumbering(t *testing.T) {
	argsFile := t.TempDir() + "/args"
	rx := NewRemuxerAt(recordingFFmpeg(t, "", argsFile))

	stop, _, _, err := rx.Segment(t.Context(), "https://cdn.example/movie.mkv", nil,
		Plan{Reason: "eac3"}, t.TempDir(), encodedSegmentLength, 10)
	if err != nil {
		t.Fatalf("Segment: %v", err)
	}
	defer stop()

	line := readEventually(t, argsFile)
	for _, want := range []string{"-ss 60.000", "-copyts", "-start_number 10", "-hls_flags temp_file", "-hls_segment_type fmp4"} {
		if !strings.Contains(line, want) {
			t.Errorf("ffmpeg args %q lack %q", line, want)
		}
	}
	if strings.Index(line, "-ss") > strings.Index(line, "-i ") {
		t.Errorf("ffmpeg args %q put -ss after -i, which decodes from zero instead of ranging the source", line)
	}
}

// TestOnlyAnEncodeForcesKeyframes is the asymmetry platform#82 rests on. Where the
// origin re-encodes it places the boundaries and the grid is exact; where it
// copies, the source's keyframes decide and forcing would be a lie the flag
// cannot make true.
func TestOnlyAnEncodeForcesKeyframes(t *testing.T) {
	for _, c := range []struct {
		name string
		plan Plan
		want bool
	}{
		{"copied video", Plan{Video: ActionCopy, Audio: ActionEncode}, false},
		{"encoded video", Plan{Video: ActionEncode, Audio: ActionCopy}, true},
	} {
		argsFile := t.TempDir() + "/args"
		rx := NewRemuxerAt(recordingFFmpeg(t, "", argsFile))
		stop, _, _, err := rx.Segment(t.Context(), "https://cdn.example/m.mkv", nil, c.plan, t.TempDir(), encodedSegmentLength, 0)
		if err != nil {
			t.Fatalf("%s: %v", c.name, err)
		}
		line := readEventually(t, argsFile)
		stop()
		if got := strings.Contains(line, "-force_key_frames"); got != c.want {
			t.Errorf("%s: -force_key_frames present = %v, want %v", c.name, got, c.want)
		}
	}
}
