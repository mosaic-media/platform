// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

// testURI is the segment naming this package's handler will use, kept here so
// the playlist tests read like the thing a client fetches.
func testURI(n int) string {
	if n == initSegment {
		return "init.mp4"
	}
	return strconv.Itoa(n) + ".m4s"
}

// segLen is the segment length these tests divide by. The origin chooses it —
// the grid is nominal, and a seek restarts at the position the playlist names
// rather than inheriting where the previous segment ended (ADR 0111) — so the
// arithmetic here is the arithmetic in production.
const segLen = encodedSegmentLength

// TestTheLastPartialSegmentIsListed is the arithmetic that decides whether the
// end of a film is reachable.
//
// A release is almost never a whole number of segments, and dropping the
// remainder loses up to six seconds off every one of them. That is the same
// class of failure the advertised byte length already had — a timeline whose
// tail cannot be reached — so it is pinned rather than assumed.
func TestTheLastPartialSegmentIsListed(t *testing.T) {
	cases := []struct {
		total time.Duration
		want  int
	}{
		{0, 0},
		{-1, 0},
		{time.Second, 1},              // shorter than one segment is still one segment
		{segLen, 1},                   // exactly one, with no empty second
		{segLen + time.Nanosecond, 2}, // a nanosecond over needs another
		{2 * segLen, 2},
		{66*time.Minute + 30*time.Second, 665}, // 3990s / 6
	}
	for _, c := range cases {
		if got := segmentCount(c.total, segLen); got != c.want {
			t.Errorf("segmentCount(%v) = %d, want %d", c.total, got, c.want)
		}
	}
}

// TestSegmentDurationsSumToTheRelease is the property that matters more than any
// single value: a player derives its timeline by adding EXTINF values, so if
// they do not add up to the probed duration the scrubber describes a film of the
// wrong length — which is exactly the disagreement that made byte addressing
// unusable, in its other form.
func TestSegmentDurationsSumToTheRelease(t *testing.T) {
	for _, total := range []time.Duration{
		90 * time.Minute,
		66*time.Minute + 30*time.Second,
		23*time.Minute + 1*time.Second,
		segLen / 2,
	} {
		var sum time.Duration
		for n := range segmentCount(total, segLen) {
			d := segmentDuration(n, total, segLen)
			if d <= 0 {
				t.Fatalf("segment %d of %v has duration %v — a listed segment must have length", n, total, d)
			}
			if d > segLen {
				t.Errorf("segment %d of %v is %v, longer than the advertised TARGETDURATION", n, total, d)
			}
			sum += d
		}
		if sum != total {
			t.Errorf("durations for %v sum to %v — a player adds these to build its timeline", total, sum)
		}
	}
}

// TestSegmentStartIsWhereFFmpegIsToldToSeek ties the index to the position. The
// index is not a name: it is the -ss value, and it is what makes ADR 0108's
// restart arithmetic survive into a segmented design.
func TestSegmentStartIsWhereFFmpegIsToldToSeek(t *testing.T) {
	if got := segmentStart(0, segLen); got != 0 {
		t.Errorf("segmentStart(0, segLen) = %v, want 0 — the first segment is the start of the film", got)
	}
	if got := segmentStart(100, segLen); got != 100*segLen {
		t.Errorf("segmentStart(100, segLen) = %v, want %v", got, 100*segLen)
	}
	// Defensive rather than expected: a negative index is a malformed request,
	// and clamping it to the start beats handing ffmpeg a negative -ss.
	if got := segmentStart(-5, segLen); got != 0 {
		t.Errorf("segmentStart(-5, segLen) = %v, want 0", got)
	}
}

// TestThePlaylistIsCompleteBeforeAnythingIsProduced is decision point 1 of
// ADR 0109, stated as a test.
//
// Every segment of the film is listed, and the playlist is closed. That is what
// lets a player seek to the last minute of a two-hour release before the
// transcode has produced its first frame — the property the advertised
// Content-Length was synthesising and could not get right.
func TestThePlaylistIsCompleteBeforeAnythingIsProduced(t *testing.T) {
	const total = 90 * time.Minute
	got := mediaPlaylist(total, segLen, testURI)

	want := segmentCount(total, segLen)
	if n := strings.Count(got, ".m4s"); n != want {
		t.Errorf("playlist lists %d segments, want all %d", n, want)
	}
	// The last segment of the film must be in the list, not merely a plausible
	// number of segments.
	if last := fmt.Sprintf("\n%d.m4s\n", want-1); !strings.Contains(got, last) {
		t.Errorf("playlist does not list the final segment %q", strings.TrimSpace(last))
	}
	if !strings.Contains(got, "#EXT-X-ENDLIST") {
		t.Error("playlist has no EXT-X-ENDLIST; without it a player treats the end as unwritten and will not seek there")
	}
	if !strings.Contains(got, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Error("playlist is not VOD; EVENT means 'more may be appended', which refuses a seek past the last listed segment")
	}
}

// TestThePlaylistDeclaresWhatFMP4Needs covers the three lines a client checks
// before it will parse the rest. Each is cheap to omit and each fails as an
// immediate, unexplained decode error rather than as a bad playlist.
func TestThePlaylistDeclaresWhatFMP4Needs(t *testing.T) {
	got := mediaPlaylist(30*time.Minute, segLen, testURI)

	for _, want := range []string{
		// Version 7 is the floor for fMP4 segments.
		"#EXT-X-VERSION:7",
		// Without the map there is no initialisation segment and no track can be
		// set up, whatever the segments contain.
		`#EXT-X-MAP:URI="init.mp4"`,
		// The ceiling must be no less than any EXTINF.
		"#EXT-X-TARGETDURATION:6",
		"#EXT-X-INDEPENDENT-SEGMENTS",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("playlist lacks %q", want)
		}
	}
	if !strings.HasPrefix(got, "#EXTM3U\n") {
		t.Error("a playlist that does not begin with #EXTM3U is not a playlist")
	}
	// One rendition, so no master and no CODECS attribute to be wrong about.
	if strings.Contains(got, "#EXT-X-STREAM-INF") {
		t.Error("playlist declares a variant stream; there is one rendition and a master would only add a guessable CODECS string")
	}
}

// TestAnUnprobedReleaseGetsNoPlaylist is the honest degradation. Without a
// duration there is nothing to divide, and emitting an empty-but-valid playlist
// would tell a player the film is zero seconds long. The caller serves the
// unseekable pipe instead, exactly as it does today.
func TestAnUnprobedReleaseGetsNoPlaylist(t *testing.T) {
	got := mediaPlaylist(0, segLen, testURI)
	if strings.Contains(got, ".m4s") {
		t.Errorf("a release with no duration produced segments:\n%s", got)
	}
}
