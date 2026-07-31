// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"sort"
	"strings"
	"time"
)

// The per-stream playback decision (ADR 0050).
//
// The framing this replaces was "remux or transcode", treating a file as wholly
// playable or wholly not. A real release disproved it: 4K HEVC video Chrome
// decoded happily, alongside four E-AC3 audio tracks it cannot decode at all.
// Whole-file transcoding would have re-encoded 32 GB of video that needed
// nothing, to fix audio that needs almost nothing.
//
// So each stream is decided on its own — and *which* stream comes first, because
// that release's first audio track is Hindi. A perfect encode of the wrong
// language is still the wrong film.

// StreamAction is what happens to one stream on the way to the client.
type StreamAction string

const (
	// ActionCopy passes the stream through untouched.
	ActionCopy StreamAction = "copy"
	// ActionEncode re-encodes it into something the client can decode.
	ActionEncode StreamAction = "encode"
	// ActionDrop leaves it out entirely.
	ActionDrop StreamAction = "drop"
)

// Plan is the decision for one playback: which streams travel, and how.
type Plan struct {
	// Video names the video stream's fate. Encoding video is the expensive case
	// and is avoided whenever the client can decode what is already there.
	Video StreamAction
	// VideoIndex is the ffmpeg stream index of the chosen video stream.
	VideoIndex int

	// Audio names the chosen audio stream's fate, and AudioIndex which stream
	// that is — the decision no whole-file view can express.
	Audio      StreamAction
	AudioIndex int
	// AudioLanguage is the language actually chosen, so a caller can say so
	// rather than leaving a user to work out why the dialogue is unexpected.
	AudioLanguage string

	// Tonemap converts high dynamic range to SDR while re-encoding. It is set
	// only alongside an ActionEncode video: there is no way to tone-map a copy.
	Tonemap bool
	// MaxHeight caps the encoded height, carried so the origin renders the same
	// decision the planner made.
	MaxHeight int

	// Duration is the release's running time, carried from the probe so the
	// origin can map a byte offset to a timestamp without re-probing on every
	// range request a scrubbing player makes.
	//
	// It is what makes a transcoded stream seekable at all. Zero means the source
	// reported none, and the origin then falls back to the unseekable pipe —
	// honestly, rather than by guessing a duration and sending a player to a
	// position that does not exist.
	Duration time.Duration

	// SourceBytes is the release's size on the wire, carried so the origin can
	// advertise a length close to what the transcode will really produce.
	//
	// It matters more than it sounds. The client derives its own byte-to-time
	// mapping from the length the origin advertises and the duration it infers
	// from the fragments, and if that disagrees with the origin's mapping a seek
	// resolves to the wrong timestamp. A flat bitrate guess disagreed by 22x on a
	// 4K release — 2 MB/s assumed against 45 MB/s actual — and the seek landed
	// nowhere near where it was asked for.
	SourceBytes int64

	// DirectPlay is true when nothing needs doing and the origin can simply
	// relay the upstream bytes, keeping byte-range seeking for free.
	DirectPlay bool

	// Subtitles are the embedded tracks this playback offers, and which of them
	// comes on by itself (ADR 0112, ADR 0113). Empty when the release has none,
	// when it was not probed, or when the stream is relayed untouched — a
	// direct-played release is the upstream's own bytes, and the origin adds no
	// wrapper it could hang a rendition off.
	//
	// It travels sealed in the ticket like the rest of the plan, so the origin
	// answers a subtitle request without re-probing and a viewer's preference
	// cannot change under a playback that is already running.
	Subtitles []SubtitleDelivery `json:"s,omitempty"`

	// Burn names a subtitle track to render into the picture (ADR 0114), or nil
	// for the ordinary case. It is set only when a rendition cannot carry the
	// track — a graphic one, or a typeset one this viewer asked to see as
	// authored — and it forces Video to ActionEncode, because frames being
	// copied through cannot be drawn on.
	//
	// Subtitles is empty whenever this is set: the burned track is in the
	// picture already, and offering it beside itself would draw it twice.
	Burn *BurnedSubtitle `json:"b,omitempty"`

	// Reason is a short human explanation of why work is needed, for logs and
	// for telling a user what is happening rather than showing a spinner.
	Reason string
}

// ClientCodecs is what the calling client can decode. It is the shape a declared
// capability profile reduces to for this decision (ADR 0047); until clients
// declare one, DefaultBrowserCodecs stands in.
type ClientCodecs struct {
	Video map[string]bool
	Audio map[string]bool
	// HDR reports whether the client can *render* high dynamic range, which is a
	// separate question from whether it can decode the codec carrying it.
	//
	// Getting this wrong is visible rather than subtle. A Dolby Vision profile 5
	// stream has no HDR10 base layer, so a decoder that ignores the DV metadata
	// renders its ICtCp data as ordinary BT.2020 and the picture comes out
	// purple and green. Treating "the client decodes HEVC" as sufficient is what
	// produced exactly that.
	HDR bool
	// MaxHeight caps the encoded output, 0 for uncapped. Only applied when the
	// video is being re-encoded anyway — downscaling a copy is not possible, and
	// re-encoding purely to shrink is not this decision's call.
	MaxHeight int
}

// DefaultBrowserCodecs is a conservative stand-in for a modern desktop Chrome
// until clients declare their own profile.
//
// It is deliberately pessimistic about audio and optimistic about nothing:
// HEVC is included because Chrome on a platform with the OS decoder plays it
// (which a live test confirmed), while AC3/E-AC3/DTS/TrueHD are excluded because
// Chrome decodes none of them in any container — that single fact is what stops
// most real releases having sound.
var DefaultBrowserCodecs = ClientCodecs{
	Video: map[string]bool{"h264": true, "vp8": true, "vp9": true, "av1": true, "hevc": true},
	Audio: map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true},
	// HDR off: a browser in a normal desktop session tone-maps nothing, and an
	// HDR stream handed to it looks wrong rather than merely flat.
	HDR: false,
	// 1080p on an encode. If the video has to be re-encoded at all, sending 4K
	// costs enormously more CPU and bandwidth than a browser window can use.
	MaxHeight: 1080,
}

// mp4Audio is the audio the origin's output container can actually carry.
//
// **It is a second constraint, and forgetting it is a real bug rather than a
// theoretical one.** The client profile answers "can this be decoded"; this
// answers "can it be muxed into what we are sending", and the two disagree in
// at least one common case. Chrome decodes FLAC, so a FLAC track was chosen and
// copied — into fragmented MP4, whose muxer refuses FLAC as experimental. ffmpeg
// exited immediately with "Could not write header for output file", and a real
// 4K release was unplayable with no error anyone could see.
//
// Anything absent here is re-encoded rather than refused: the audio encode is
// cheap and already the path for a codec the client cannot decode, so an
// unmuxable one costs nothing new. Opus and ALAC are present because the MP4
// muxer takes both without -strict; FLAC, Vorbis, PCM and the lossless
// passthrough formats are not.
var mp4Audio = map[string]bool{
	"aac":  true,
	"mp3":  true,
	"ac3":  true,
	"eac3": true,
	"opus": true,
	"alac": true,
}

// PreferredLanguages is the order audio tracks are chosen in, most wanted first.
// It is a placeholder for a real user preference: language belongs to a person,
// not to an install, and this is the shape that preference will slot into.
var PreferredLanguages = []string{"eng", "en"}

// Decide builds a plan for playing info on a client with codecs.
func Decide(info MediaInfo, codecs ClientCodecs, preferred []string) Plan {
	if len(preferred) == 0 {
		preferred = PreferredLanguages
	}

	plan := Plan{
		Video:       ActionCopy,
		VideoIndex:  info.Video.Index,
		Audio:       ActionDrop,
		AudioIndex:  -1,
		MaxHeight:   codecs.MaxHeight,
		Duration:    info.Duration,
		SourceBytes: info.SizeBytes,
	}

	switch {
	case info.Video.Codec != "" && !codecs.Video[strings.ToLower(info.Video.Codec)]:
		plan.Video = ActionEncode
		plan.Reason = "video codec " + info.Video.Codec + " is not decodable by this client"
	case info.Video.HDRFormat != "" && !codecs.HDR:
		// Decodable, and still wrong to pass through. This is the purple-and-green
		// case: the client will happily decode the stream and render its colours
		// as nonsense, which is worse than refusing it, because it looks like a
		// broken file rather than an unsupported format.
		plan.Video = ActionEncode
		plan.Tonemap = true
		plan.Reason = info.Video.HDRFormat + " needs tone-mapping for this client"
	}

	if track, ok := chooseAudio(info.Audio, preferred); ok {
		codec := strings.ToLower(track.Codec)
		plan.AudioIndex = track.Index
		plan.AudioLanguage = track.Language
		if codecs.Audio[codec] {
			plan.Audio = ActionCopy
		} else {
			plan.Audio = ActionEncode
			if plan.Reason == "" {
				plan.Reason = "audio codec " + track.Codec + " is not decodable by this client"
			} else {
				plan.Reason += "; audio codec " + track.Codec + " is not either"
			}
		}

		// The output container's constraint, and it applies **only once ffmpeg is
		// already involved**. While the stream is relayed untouched the container
		// is the source's own, and Matroska carries FLAC perfectly well — forcing
		// an encode there would take a release that direct-plays today and push
		// it through a transcode for nothing.
		//
		// The moment anything else forces a remux, the output is fragmented MP4
		// and the question changes from "can the client decode it" to "can this
		// container carry it". FLAC answers yes to the first and no to the
		// second, which is how a 4K release with an 8-channel FLAC track met a
		// tone-map it needed, went to ffmpeg, and died on "flac in MP4 support is
		// experimental" before writing a single byte.
		if plan.Audio == ActionCopy && plan.Video == ActionEncode && !mp4Audio[codec] {
			plan.Audio = ActionEncode
			plan.Reason += "; audio codec " + track.Codec + " cannot be carried by the MP4 output"
		}
	}

	// Direct play is the absence of work, not a separate mode. A release with no
	// audio at all still direct-plays: silence that was always silent is not a
	// problem to solve.
	plan.DirectPlay = plan.Video == ActionCopy &&
		(plan.Audio == ActionCopy || plan.Audio == ActionDrop && len(info.Audio) == 0)

	return plan
}

// chooseAudio picks the audio track to play.
//
// Order matters and the first track is a poor default: a multi-language release
// routinely leads with a language the viewer does not speak. Preference wins
// first, then the container's own default flag, then — only as a last resort —
// the first track.
func chooseAudio(tracks []AudioTrack, preferred []string) (AudioTrack, bool) {
	if len(tracks) == 0 {
		return AudioTrack{}, false
	}

	rank := func(t AudioTrack) int {
		for i, want := range preferred {
			if t.Language == strings.ToLower(want) {
				return i
			}
		}
		// An untagged track is treated as *possible* rather than wrong: a
		// single-audio release often carries no language tag at all, and
		// ranking it below a tagged foreign track would pick the wrong one.
		if t.Language == "" || t.Language == "und" {
			return len(preferred)
		}
		return len(preferred) + 1
	}

	ordered := make([]AudioTrack, len(tracks))
	copy(ordered, tracks)
	sort.SliceStable(ordered, func(i, j int) bool {
		ri, rj := rank(ordered[i]), rank(ordered[j])
		if ri != rj {
			return ri < rj
		}
		// Within the same language, prefer the track the release marked default,
		// then the one with more channels — a 5.1 mix over a stereo commentary.
		if ordered[i].Default != ordered[j].Default {
			return ordered[i].Default
		}
		return ordered[i].Channels > ordered[j].Channels
	})
	return ordered[0], true
}
