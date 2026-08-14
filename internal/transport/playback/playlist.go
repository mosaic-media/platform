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

// The segmented transcode's playlist (platform#82).
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
// deliberate: this is the part of platform#82 that is pure arithmetic over a
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
// **Where the video is copied it is what ffmpeg is asked for and not what it
// produces** — the source's keyframes decide, so a release with ten-second
// keyframes yields ten-second segments however this is set. The nominal grid
// tolerates that, because a seek restarts at the position the playlist names
// rather than inheriting where the previous segment happened to end
// (platform#82).
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

// HLSMimeType is the same value, named for the client rather than the response.
// It travels on the Player node so a client chooses its pipeline before it
// fetches anything — Safari plays this natively and everything else needs a
// media framework (web#5's stated condition).
const HLSMimeType = mediaPlaylistType

// mediaPlaylist renders the whole playlist for a release of this length.
//
// **There is no master playlist, because there is one rendition.** A master
// exists to let a client choose between variants, and Mosaic serves exactly one:
// platform#82 puts a real bitrate ladder out of scope, because a menu of unrelated
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
	// Sprintf rather than Fprintf throughout, because platform#31's gate bans
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

// videoPlaylistName is where the video's own media playlist moves to once a
// master exists above it. Without subtitles there is no master and the media
// playlist keeps the entry-point name, so a release with none is served exactly
// the bytes it was before (platform#83).
const videoPlaylistName = "v.m3u8"

// masterPlaylist declares the video rendition and one subtitle rendition per
// offered track (platform#83).
//
// A master is emitted only when there are subtitles to declare. The comment on
// mediaPlaylist gives the reasons not to have one — a round trip, and a CODECS
// string the origin can only guess at before the transcode runs — and neither is
// answered by this; they are outweighed. **A subtitle rendition is the one thing
// a player cannot be told about any other way**, and being told about it through
// HLS is what makes subtitles cost no client change at all: the player already
// has a menu, a track selector and a renderer for them.
//
// CODECS is still omitted, so the objection that mattered is avoided rather than
// accepted: a wrong CODECS makes a browser refuse a stream, and an absent one
// makes it look at what arrives. BANDWIDTH is not optional in the grammar, so it
// is derived from the source's own size and duration — the honest estimate,
// which is used by nothing here because there is no ladder to choose from.
func masterPlaylist(bandwidth int, subtitles []SubtitleDelivery) string {
	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-INDEPENDENT-SEGMENTS\n")

	for i, s := range subtitles {
		b.WriteString("#EXT-X-MEDIA:TYPE=SUBTITLES,GROUP-ID=\"subs\"")
		b.WriteString(fmt.Sprintf(",NAME=%q", s.Label))
		if s.Language != "" {
			b.WriteString(fmt.Sprintf(",LANGUAGE=%q", s.Language))
		}
		// DEFAULT is the whole delivery of platform#83: the escalation decided
		// which track comes on, and this attribute is where that decision
		// reaches the player. AUTOSELECT tracks it, so a client choosing by the
		// system language does not overrule a choice the viewer already made.
		b.WriteString(",DEFAULT=" + yesNo(s.Default))
		b.WriteString(",AUTOSELECT=" + yesNo(s.Default))
		// FORCED means the player may show this without the viewer asking, which
		// is exactly what a forced track is for. It is a property of the track
		// rather than of the choice, so it is set on every forced rendition and
		// not only on the default one.
		b.WriteString(",FORCED=" + yesNo(s.Forced))
		b.WriteString(fmt.Sprintf(",URI=%q\n", subtitlePlaylistName(i)))
	}

	b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,SUBTITLES=\"subs\"\n", bandwidth))
	b.WriteString(videoPlaylistName + "\n")
	return b.String()
}

// yesNo renders an HLS attribute's enumerated boolean, which is spelled out
// rather than 0/1 and is not quoted.
func yesNo(v bool) string {
	if v {
		return "YES"
	}
	return "NO"
}

// masterBandwidth estimates the stream's bit rate for the master's required
// BANDWIDTH attribute.
//
// The source's size over its duration, which is what the release actually is on
// the wire. It is wrong for a re-encoded stream — that is the same unknowable
// the byte-addressed origin foundered on — and here it costs nothing, because a
// master with one variant offers no choice for a wrong number to influence. The
// fallback is a plausible 1080p rate for the case where the source reported no
// size, and is equally inconsequential.
func masterBandwidth(sourceBytes int64, duration time.Duration) int {
	if sourceBytes <= 0 || duration <= 0 {
		return 8_000_000
	}
	return int(float64(sourceBytes) * 8 / duration.Seconds())
}

// subtitlePlaylist renders one subtitle rendition, on its own grid.
//
// It describes the same running time as the video and divides it differently,
// which HLS permits and which is the point: the segment length here is chosen
// against the cost of extracting text out of a container rather than against the
// cost of seeking video.
//
// There is no EXT-X-MAP. WebVTT segments are self-describing — each is a
// complete little document with its own header — so there is no initialisation
// segment to point at, which is also why they can be produced by a process that
// starts anywhere in the release.
func subtitlePlaylist(total time.Duration, track int) string {
	count := segmentCount(total, subtitleWindow)

	var b strings.Builder
	b.WriteString("#EXTM3U\n")
	b.WriteString("#EXT-X-VERSION:7\n")
	b.WriteString("#EXT-X-PLAYLIST-TYPE:VOD\n")
	b.WriteString(fmt.Sprintf("#EXT-X-TARGETDURATION:%d\n", int(math.Ceil(subtitleWindow.Seconds()))))
	b.WriteString("#EXT-X-MEDIA-SEQUENCE:0\n")
	for n := range count {
		b.WriteString(fmt.Sprintf("#EXTINF:%.6f,\n", segmentDuration(n, total, subtitleWindow).Seconds()))
		b.WriteString(subtitleSegmentName(track, n))
		b.WriteString("\n")
	}
	b.WriteString("#EXT-X-ENDLIST\n")
	return b.String()
}

// vttMimeType is what a WebVTT segment is served as.
const vttMimeType = "text/vtt"
