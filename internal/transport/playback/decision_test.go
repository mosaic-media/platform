// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"slices"
	"strings"
	"testing"
)

// theRealRelease is the file that produced this whole design: a 32 GB 4K HDR
// Matroska that Chrome played with no sound. Four E-AC3 audio tracks, and the
// first one is Hindi.
var theRealRelease = MediaInfo{
	Container: "matroska",
	SizeBytes: 31_980_000_000,
	Video:     VideoTrack{Index: 0, Codec: "hevc", Width: 3840, Height: 2160, Profile: "Main 10", HDRFormat: "HDR10"},
	Audio: []AudioTrack{
		{Index: 1, Codec: "eac3", Channels: 6, Language: "hin", Default: true},
		{Index: 2, Codec: "eac3", Channels: 6, Language: "tam"},
		{Index: 3, Codec: "eac3", Channels: 6, Language: "tel"},
		{Index: 4, Codec: "eac3", Channels: 6, Language: "eng"},
	},
	Subtitles: []SubtitleTrack{{Index: 5, Codec: "subrip", Language: "eng"}},
}

// TestDecideCopiesVideoAndEncodesOnlyTheAudio is the whole point of deciding per
// stream. Re-encoding 32 GB of 4K HEVC to fix an audio track would not keep up
// on a home server, and it is not necessary: Chrome decodes the video already.
func TestDecideCopiesVideoAndEncodesOnlyTheAudio(t *testing.T) {
	plan := Decide(theRealRelease, DefaultBrowserCodecs, nil)

	if plan.Video != ActionCopy {
		t.Errorf("video = %q, want %q — the client decodes HEVC, so re-encoding it is wasted work", plan.Video, ActionCopy)
	}
	if plan.Audio != ActionEncode {
		t.Errorf("audio = %q, want %q — Chrome decodes no E-AC3 in any container", plan.Audio, ActionEncode)
	}
	if plan.DirectPlay {
		t.Error("DirectPlay must be false: relaying this is what produced a silent film")
	}
	if !strings.Contains(plan.Reason, "eac3") {
		t.Errorf("Reason = %q, should name the codec that forced the work", plan.Reason)
	}
}

// TestDecideChoosesEnglishNotTheFirstTrack pins the requirement only a real file
// surfaced. Mapping 0:a:0 gives Hindi audio on an English film — a perfect
// encode of the wrong language is still the wrong film.
func TestDecideChoosesEnglishNotTheFirstTrack(t *testing.T) {
	plan := Decide(theRealRelease, DefaultBrowserCodecs, nil)

	if plan.AudioLanguage != "eng" {
		t.Fatalf("chose %q audio, want %q", plan.AudioLanguage, "eng")
	}
	if plan.AudioIndex != 4 {
		t.Errorf("AudioIndex = %d, want 4 (the English track, not the first or the default)", plan.AudioIndex)
	}
	// The Hindi track carries the container's default flag. Preference has to
	// beat it, or every multi-language release plays in the wrong language.
	if plan.AudioIndex == 1 {
		t.Error("picked the default-flagged track over the preferred language")
	}
}

// TestDecideDirectPlaysWhenNothingNeedsDoing guards the case that must stay
// cheap: a fully compatible release is relayed, which keeps byte-range seeking
// that an ffmpeg pipe cannot offer.
func TestDecideDirectPlaysWhenNothingNeedsDoing(t *testing.T) {
	info := MediaInfo{
		Container: "mov",
		Video:     VideoTrack{Index: 0, Codec: "h264"},
		Audio:     []AudioTrack{{Index: 1, Codec: "aac", Channels: 2, Language: "eng"}},
	}
	plan := Decide(info, DefaultBrowserCodecs, nil)

	if !plan.DirectPlay {
		t.Fatalf("want direct play, got video=%q audio=%q reason=%q", plan.Video, plan.Audio, plan.Reason)
	}
	if plan.Video != ActionCopy || plan.Audio != ActionCopy {
		t.Errorf("nothing should be re-encoded: video=%q audio=%q", plan.Video, plan.Audio)
	}
}

// TestDecideEncodesVideoOnlyWhenItMust — a client without HEVC is the one case
// where the expensive operation is unavoidable.
func TestDecideEncodesVideoOnlyWhenItMust(t *testing.T) {
	noHEVC := ClientCodecs{
		Video: map[string]bool{"h264": true},
		Audio: map[string]bool{"aac": true},
	}
	plan := Decide(theRealRelease, noHEVC, nil)

	if plan.Video != ActionEncode {
		t.Errorf("video = %q, want %q for a client with no HEVC", plan.Video, ActionEncode)
	}
	if !strings.Contains(plan.Reason, "hevc") {
		t.Errorf("Reason = %q, should name the video codec", plan.Reason)
	}
}

// TestChooseAudioPrefersUntaggedOverAWrongLanguage covers the single-audio
// release, which very often carries no language tag at all. Ranking an untagged
// track below a tagged foreign one would pick the wrong track on a file that
// only ever had one sensible answer.
func TestChooseAudioPrefersUntaggedOverAWrongLanguage(t *testing.T) {
	got, ok := chooseAudio([]AudioTrack{
		{Index: 1, Codec: "aac", Language: "fre"},
		{Index: 2, Codec: "aac", Language: ""},
	}, PreferredLanguages)
	if !ok || got.Index != 2 {
		t.Fatalf("chose index %d, want the untagged track (2)", got.Index)
	}
}

// TestChooseAudioPrefersMoreChannelsWithinALanguage keeps a stereo commentary
// track from beating the 5.1 feature mix.
func TestChooseAudioPrefersMoreChannelsWithinALanguage(t *testing.T) {
	got, _ := chooseAudio([]AudioTrack{
		{Index: 1, Codec: "aac", Language: "eng", Channels: 2},
		{Index: 2, Codec: "aac", Language: "eng", Channels: 6},
	}, PreferredLanguages)
	if got.Index != 2 {
		t.Errorf("chose index %d (%d ch), want the 5.1 track", got.Index, got.Channels)
	}
}

// TestPlanArgsCopyVideoEncodeAudio checks the flags the plan actually renders,
// because a correct decision expressed as the wrong ffmpeg invocation is still
// a silent film.
func TestPlanArgsCopyVideoEncodeAudio(t *testing.T) {
	args := Decide(theRealRelease, DefaultBrowserCodecs, nil).ffmpegArgs()

	if !hasPair(args, "-c:v", "copy") {
		t.Errorf("args %v must copy video", args)
	}
	if !hasPair(args, "-c:a", "aac") {
		t.Errorf("args %v must encode audio to aac", args)
	}
	if !hasPair(args, "-map", "0:4") {
		t.Errorf("args %v must map the English audio stream (0:4)", args)
	}
	if !slices.Contains(args, "-sn") {
		t.Error("subtitles must be excluded: SubRip and ASS have no MP4 mapping and would fail the whole command")
	}
}

func hasPair(args []string, flag, value string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == value {
			return true
		}
	}
	return false
}
