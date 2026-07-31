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

// sameReleaseInSDR is theRealRelease with the dynamic range removed.
//
// It exists because the copy-video case cannot be demonstrated on an HDR source
// any more, and that is a correction rather than a test inconvenience: these
// assertions used to run against the HDR fixture and pass, which means they were
// asserting a copy that would have rendered with wrong colour. Tone-mapping is
// the *only* honest answer for HDR on an SDR client, so the "copy the video"
// case needs a source where copying is genuinely right.
var sameReleaseInSDR = func() MediaInfo {
	m := theRealRelease
	m.Video.HDRFormat = ""
	return m
}()

// TestDecideCopiesVideoAndEncodesOnlyTheAudio is the whole point of deciding per
// stream. Re-encoding 32 GB of 4K HEVC to fix an audio track would not keep up
// on a home server, and it is not necessary: Chrome decodes the video already.
func TestDecideCopiesVideoAndEncodesOnlyTheAudio(t *testing.T) {
	plan := Decide(sameReleaseInSDR, DefaultBrowserCodecs, nil)

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
	plan := Decide(sameReleaseInSDR, DefaultBrowserCodecs, nil)

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
	args := Decide(sameReleaseInSDR, DefaultBrowserCodecs, nil).ffmpegArgs("https://cdn.example/movie.mkv")

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

// TestHDRVideoIsNeverCopiedToAnSDRClient is the purple-and-green bug, pinned.
//
// The client decodes HEVC, so the codec check passes and the earlier code copied
// the stream straight through. A Dolby Vision profile 5 stream has no HDR10 base
// layer, so the browser rendered its ICtCp data as BT.2020 — a picture that
// looks broken rather than unsupported, which is the worse failure.
func TestHDRVideoIsNeverCopiedToAnSDRClient(t *testing.T) {
	dolbyVision := MediaInfo{
		Container: "matroska",
		Video:     VideoTrack{Index: 0, Codec: "hevc", Width: 3840, Height: 2160, HDRFormat: "DolbyVision"},
		Audio:     []AudioTrack{{Index: 1, Codec: "aac", Language: "eng"}},
	}

	plan := Decide(dolbyVision, DefaultBrowserCodecs, nil)

	if plan.Video != ActionEncode {
		t.Fatalf("video = %q, want %q — a decodable codec is not the same as a renderable picture", plan.Video, ActionEncode)
	}
	if !plan.Tonemap {
		t.Error("an HDR source encoded for an SDR client must be tone-mapped, or the colour is still wrong")
	}
	if plan.DirectPlay {
		t.Error("DirectPlay must be false: relaying this is what produced the purple-and-green picture")
	}
	// The audio was fine and must not be dragged into the re-encode.
	if plan.Audio != ActionCopy {
		t.Errorf("audio = %q, want %q — it was already decodable", plan.Audio, ActionCopy)
	}
}

// TestHDRIsCopiedToAClientThatCanRenderIt — the same source must not be
// needlessly re-encoded for a client that handles HDR, which is the whole
// reason this is a client property rather than a blanket rule.
func TestHDRIsCopiedToAClientThatCanRenderIt(t *testing.T) {
	hdrClient := DefaultBrowserCodecs
	hdrClient.HDR = true

	plan := Decide(MediaInfo{
		Video: VideoTrack{Index: 0, Codec: "hevc", Height: 2160, HDRFormat: "HDR10"},
		Audio: []AudioTrack{{Index: 1, Codec: "aac", Language: "eng"}},
	}, hdrClient, nil)

	if plan.Video != ActionCopy || plan.Tonemap {
		t.Errorf("video = %q tonemap=%v, want a plain copy for an HDR-capable client", plan.Video, plan.Tonemap)
	}
}

// TestTonemapFilterDownscalesBeforeMapping — tone-mapping is per-pixel, so doing
// it after a 4K-to-1080p reduction is about a quarter of the work. Order matters
// enough to assert.
func TestTonemapFilterDownscalesBeforeMapping(t *testing.T) {
	vf := Plan{Video: ActionEncode, Tonemap: true, MaxHeight: 1080}.videoFilter()

	scale := strings.Index(vf, "scale=-2")
	tone := strings.Index(vf, "tonemap=")
	if scale < 0 || tone < 0 {
		t.Fatalf("filter chain missing scale or tonemap: %q", vf)
	}
	if scale > tone {
		t.Errorf("scale must precede tonemap (four times the pixels otherwise): %q", vf)
	}
	if !strings.HasSuffix(vf, "format=yuv420p") {
		t.Errorf("chain must end in yuv420p or the browser cannot decode it: %q", vf)
	}
}

// TestNoFilterWhenCopying — a copied stream takes no filter chain at all;
// passing one to ffmpeg alongside `-c:v copy` is an error, not a no-op.
func TestNoFilterWhenCopying(t *testing.T) {
	if vf := (Plan{Video: ActionCopy}).videoFilter(); vf != "" {
		t.Errorf("videoFilter() = %q, want empty for a copy", vf)
	}
}

// TestUnmuxableAudioIsEncodedOnlyWhenRemuxing pins the two-constraint rule that
// made a real 4K release unplayable.
//
// The client profile answers "can this be decoded"; the output container answers
// "can this be carried". Chrome decodes FLAC, so a FLAC track was chosen and
// copied — into fragmented MP4, whose muxer refuses FLAC as experimental. ffmpeg
// exited before writing a byte.
//
// The second half of the name is the part that is easy to get wrong, and the
// first attempt at this fix did: the container constraint must apply *only* when
// ffmpeg is already involved. While the stream is relayed the container is the
// source's own, and Matroska carries FLAC perfectly well — forcing an encode
// there would push a release that direct-plays today through a transcode for
// nothing.
func TestUnmuxableAudioIsEncodedOnlyWhenRemuxing(t *testing.T) {
	flac := []AudioTrack{{Index: 1, Codec: "flac", Language: "eng"}}

	// Relayed: nothing forces ffmpeg, so FLAC rides along in the source's own
	// container and the whole thing direct-plays.
	relayed := Decide(
		MediaInfo{Video: VideoTrack{Index: 0, Codec: "h264"}, Audio: flac},
		DefaultBrowserCodecs, nil)
	if relayed.Audio != ActionCopy {
		t.Errorf("Audio = %q, want %q — a relayed stream keeps the source container, which carries FLAC",
			relayed.Audio, ActionCopy)
	}
	if !relayed.DirectPlay {
		t.Errorf("DirectPlay = false; forcing a transcode here costs a working direct play for nothing")
	}

	// Remuxing: the tone-map forces ffmpeg, the output becomes MP4, and FLAC
	// cannot go in it.
	remuxed := Decide(
		MediaInfo{Video: VideoTrack{Index: 0, Codec: "hevc", HDRFormat: "HDR10"}, Audio: flac},
		DefaultBrowserCodecs, nil)
	if remuxed.Video != ActionEncode {
		t.Fatalf("Video = %q, want %q — HDR10 to a non-HDR client needs tone-mapping", remuxed.Video, ActionEncode)
	}
	if remuxed.Audio != ActionEncode {
		t.Errorf("Audio = %q, want %q — MP4 cannot carry FLAC, and copying it kills ffmpeg at header-write",
			remuxed.Audio, ActionEncode)
	}
	// The reason has to name the container, not the client: "not decodable by
	// this client" would be false here and would send someone to the wrong place.
	if !strings.Contains(remuxed.Reason, "MP4") {
		t.Errorf("Reason = %q, want it to name the output container as the constraint", remuxed.Reason)
	}
}

// AC3 is the mirror case and guards the rule from being written as "encode
// anything unusual": the client cannot decode it, so it is encoded — but for the
// client's reason, since MP4 carries AC3 perfectly well.
func TestUndecodableAudioReasonNamesTheClientNotTheContainer(t *testing.T) {
	plan := Decide(
		MediaInfo{Video: VideoTrack{Index: 0, Codec: "h264"}, Audio: []AudioTrack{{Index: 1, Codec: "ac3", Language: "eng"}}},
		DefaultBrowserCodecs, nil)
	if plan.Audio != ActionEncode {
		t.Fatalf("Audio = %q, want %q", plan.Audio, ActionEncode)
	}
	if strings.Contains(plan.Reason, "MP4") {
		t.Errorf("Reason = %q, but MP4 carries AC3 — the constraint here is the client's decoder", plan.Reason)
	}
}
