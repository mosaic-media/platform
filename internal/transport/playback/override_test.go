// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import "testing"

// The per-play override (platform#71): a preference decides the default, an
// override decides one sitting, and neither writes to the other.

func ptr(v int) *int { return &v }

// browserish is a client that decodes AAC and not DTS, which is what makes an
// override's real cost visible.
func browserish() ClientCodecs {
	return ClientCodecs{
		Video: map[string]bool{"h264": true},
		Audio: map[string]bool{"aac": true},
	}
}

func twoTrackRelease() MediaInfo {
	return MediaInfo{
		Video: VideoTrack{Index: 0, Codec: "h264"},
		Audio: []AudioTrack{
			{Index: 1, Codec: "aac", Language: "eng"},
			{Index: 2, Codec: "dts", Language: "jpn"},
		},
	}
}

// TestAnOverrideReDecidesRatherThanRelabels is the property that makes this
// safe. Whether audio is copied or encoded is a fact about the *chosen track's*
// codec, so a plan that carried a new index beside the old verdict would copy a
// stream the client cannot decode and present it to a viewer as silence.
func TestAnOverrideReDecidesRatherThanRelabels(t *testing.T) {
	info := twoTrackRelease()
	base := Decide(info, browserish(), []string{"eng"})
	if base.AudioIndex != 1 || base.Audio != ActionCopy || !base.DirectPlay {
		t.Fatalf("fixture: base plan = %+v, want the AAC track copied and direct-played", base)
	}

	// Switching to the DTS track turns a direct play into a transcode, and the
	// plan has to say so.
	got := WithAudioOverride(base, info, browserish(), ptr(2))
	if got.AudioIndex != 2 || got.AudioLanguage != "jpn" {
		t.Errorf("override chose %d/%q, want stream 2 in Japanese", got.AudioIndex, got.AudioLanguage)
	}
	if got.Audio != ActionEncode {
		t.Errorf("audio action = %q, want an encode — the client cannot decode DTS", got.Audio)
	}
	if got.DirectPlay {
		t.Error("still reported as a direct play, which would relay a stream the client cannot decode")
	}
	if got.Reason == "" {
		t.Error("no reason given for work the viewer's own choice caused")
	}
}

// TestAnOverrideBackToACheapTrackIsCheapAgain is the same property in the other
// direction. Nothing about an override should leave a plan permanently
// pessimistic.
func TestAnOverrideBackToACheapTrackIsCheapAgain(t *testing.T) {
	info := twoTrackRelease()
	base := Decide(info, browserish(), []string{"jpn"})
	if base.AudioIndex != 2 || base.Audio != ActionEncode {
		t.Fatalf("fixture: base plan = %+v, want the DTS track encoded", base)
	}

	got := WithAudioOverride(base, info, browserish(), ptr(1))
	if got.Audio != ActionCopy || !got.DirectPlay {
		t.Errorf("plan = %+v, want the AAC track copied and direct-played again", got)
	}
}

// TestAnOverrideNamingNothingLeavesThePlanAlone covers a stale menu. The tracks
// a client lists came from a probe, and a release can be re-probed under it;
// losing the audio entirely is a worse answer than ignoring the choice.
func TestAnOverrideNamingNothingLeavesThePlanAlone(t *testing.T) {
	info := twoTrackRelease()
	base := Decide(info, browserish(), []string{"eng"})

	for _, index := range []*int{nil, ptr(99), ptr(-1)} {
		got := WithAudioOverride(base, info, browserish(), index)
		if got.AudioIndex != base.AudioIndex || got.Audio != base.Audio || got.DirectPlay != base.DirectPlay {
			t.Errorf("index %v changed the plan to %+v, want it untouched", index, got)
		}
	}
}

// TestASubtitleOverrideMovesTheDefaultAndNothingElse is the subtitle half. An
// override is a choice among what is offered, not a new opinion about what
// should be offered — so the same tracks are listed, in the same order.
func TestASubtitleOverrideMovesTheDefaultAndNothingElse(t *testing.T) {
	offeredTracks, _, styledTracks := DecideSubtitles([]SubtitleTrack{
		{Index: 3, Codec: "subrip", Language: "eng"},
		{Index: 4, Codec: "subrip", Language: "jpn"},
	}, SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng"}}, StylingPlain)

	if len(offeredTracks) != 2 || !offeredTracks[0].Default {
		t.Fatalf("fixture: offered = %+v, want English on", offeredTracks)
	}

	WithSubtitleOverride(offeredTracks, styledTracks, ptr(4))
	if offeredTracks[0].Default {
		t.Error("the preference's track is still default after an override")
	}
	if !offeredTracks[1].Default {
		t.Error("the overridden track is not on")
	}
	if len(offeredTracks) != 2 {
		t.Errorf("offered %d tracks after an override, want both still listed", len(offeredTracks))
	}

	// And an index naming nothing offered leaves the preference alone.
	WithSubtitleOverride(offeredTracks, styledTracks, ptr(99))
	if !offeredTracks[1].Default {
		t.Error("an unknown index cleared the default instead of being ignored")
	}
}

// TestAnOverrideDoesNotBecomeAPreference is the bound that keeps the two
// mechanisms separate. Sampling the Japanese audio on one episode has not
// changed what somebody wants on the next, and there is nothing here that
// writes — this test exists so that stays true if someone adds a store.
func TestAnOverrideDoesNotBecomeAPreference(t *testing.T) {
	pref := LanguagePreference{Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesForced}
	before := pref

	info := twoTrackRelease()
	base := Decide(info, browserish(), pref.Audio)
	_ = WithAudioOverride(base, info, browserish(), ptr(2))

	if pref.SubtitleMode != before.SubtitleMode || len(pref.Audio) != len(before.Audio) || pref.Audio[0] != before.Audio[0] {
		t.Errorf("the preference changed from %+v to %+v; an override is one sitting", before, pref)
	}
}
