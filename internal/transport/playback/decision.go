// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"sort"
	"strings"
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

	// DirectPlay is true when nothing needs doing and the origin can simply
	// relay the upstream bytes, keeping byte-range seeking for free.
	DirectPlay bool

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
		Video:      ActionCopy,
		VideoIndex: info.Video.Index,
		Audio:      ActionDrop,
		AudioIndex: -1,
	}

	if info.Video.Codec != "" && !codecs.Video[strings.ToLower(info.Video.Codec)] {
		plan.Video = ActionEncode
		plan.Reason = "video codec " + info.Video.Codec + " is not decodable by this client"
	}

	if track, ok := chooseAudio(info.Audio, preferred); ok {
		plan.AudioIndex = track.Index
		plan.AudioLanguage = track.Language
		if codecs.Audio[strings.ToLower(track.Codec)] {
			plan.Audio = ActionCopy
		} else {
			plan.Audio = ActionEncode
			if plan.Reason == "" {
				plan.Reason = "audio codec " + track.Codec + " is not decodable by this client"
			} else {
				plan.Reason += "; audio codec " + track.Codec + " is not either"
			}
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
