// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"fmt"
	"math"
	"strings"
	"time"
)

// The segmented transcode's playlist (ADR 0109).
//
// **It is computed, not observed**, and that is the whole reason segmenting
// replaces byte addressing. The origin cannot know how many bytes a transcode
// will produce — it advertised the source's size and then wrote whatever ffmpeg
// made of it, which for a re-encoded release differs by an order of magnitude —
// but it does know how *long* the release is, because ffprobe measured it. A
// playlist is a statement about time, so it can be emitted in full before a
// single byte exists, and a player can seek anywhere in it immediately.
//
// Nothing here starts a transcode or touches ffmpeg. That separation is
// deliberate: this is the part of ADR 0109 that is pure arithmetic over a
// duration, and it is the part that can be checked without a decoder — which
// matters in a slice whose every previous design passed its unit tests and
// failed in a browser.

// encodedSegmentLength is the segment length the origin chooses when it is
// re-encoding the video and therefore places the keyframes itself.
//
// Six, following Jellyfin and `remux`. It bounds two costs in opposite
// directions: a seek to an unproduced position costs one ffmpeg restart, so
// shorter segments make a scrub cheaper, while every segment costs a container
// header and a request, so longer ones make a play cheaper. Neither is near a
// cliff at six.
//
// **It applies only where the boundaries are ours** (ADR 0110). Where the video
// is copied the source's keyframe spacing decides, and this constant has no say
// — asking for six seconds of a release with ten-second keyframes produces
// ten-second segments and a playlist describing positions that do not exist.
const encodedSegmentLength = 6 * time.Second

// segmentCount is how many segments a release of this length divides into.
//
// The last one is short whenever the duration is not a whole multiple, and it is
// still a segment: dropping it would make the final seconds of every film
// unreachable, which is the failure the advertised-length estimate already had.
func segmentCount(total, length time.Duration) int {
	if total <= 0 || length <= 0 {
		return 0
	}
	n := int(total / length)
	if total%length > 0 {
		n++
	}
	return n
}

// segmentStart is where segment n begins in the release. It is the value handed
// to ffmpeg's -ss, and the reason a segment index is a position rather than an
// opaque name.
func segmentStart(n int, length time.Duration) time.Duration {
	if n <= 0 || length <= 0 {
		return 0
	}
	return time.Duration(n) * length
}

// segmentDuration is how long segment n runs, which is a full segment except
// for the last.
func segmentDuration(n int, total, length time.Duration) time.Duration {
	start := segmentStart(n, length)
	if start >= total || length <= 0 {
		return 0
	}
	if remaining := total - start; remaining < length {
		return remaining
	}
	return length
}

// mediaPlaylistType is what the origin serves a playlist as. A client that gets
// this wrong treats the playlist as a file to play rather than an index to
// follow, which presents as an immediate decode failure.
const mediaPlaylistType = "application/vnd.apple.mpegurl"

// mediaPlaylist renders the whole playlist for a release of this length.
//
// **There is no master playlist, because there is one rendition.** A master
// exists to let a client choose between variants, and Mosaic serves exactly one:
// ADR 0109 puts a real bitrate ladder out of scope, because a menu of unrelated
// releases cannot supply aligned renditions at any level of effort. Emitting a
// master anyway would cost a round trip and would require declaring a CODECS
// string the origin can only guess at before the transcode runs — and a wrong
// CODECS is worse than none, since a browser refuses a stream it was told to
// expect in a codec that does not arrive. Both hls.js and Safari accept a media
// playlist directly.
//
// `VOD` with a closing `EXT-X-ENDLIST` is the load-bearing part. An `EVENT`
// playlist says "more may be appended", which makes a player treat everything
// past the last listed segment as not yet existing and refuse to seek there.
// Declaring the whole thing up front is what makes an unproduced position
// seekable, and it is honest: the segment list is derived from a duration that
// was measured, not from output that has been observed.
func mediaPlaylist(total, length time.Duration, segmentURI func(n int) string) string {
	count := segmentCount(total, length)

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	// Version 7 is the floor for fragmented-MP4 segments, which is what the
	// origin emits and what Apple's authoring rules require for HEVC.
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	// The ceiling the spec requires be no less than any EXTINF, rounded up —
	// a measured keyframe interval is rarely a whole number of seconds, and a
	// truncated ceiling would sit under the segments it is supposed to bound.
	//
	// Sprintf rather than Fprintf throughout, because ADR 0053's gate bans
	// every fmt function that emits — deliberately coarsely, since it cannot
	// tell a strings.Builder from os.Stderr — and Sprintf is the formatter it
	// leaves alone.
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(length.Seconds()))))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	// Every segment carries its own keyframe, so a player may start at any of
	// them — which is the property that makes seeking work at all here.
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")
	b.WriteString(fmt.Sprintf("#EXT-X-MAP:URI=%q\n", segmentURI(initSegment)))

	for n := range count {
		b.WriteString(fmt.Sprintf("#EXTINF:%.6f,\n", segmentDuration(n, total, length).Seconds()))
		b.WriteString(segmentURI(n))
		b.WriteString("\n")
	}

	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// initSegment names the fMP4 initialisation segment in the same space as the
// media segments, so one function renders every URI in the playlist and they
// cannot drift apart.
const initSegment = -1
