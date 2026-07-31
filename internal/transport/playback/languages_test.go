// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import (
	"reflect"
	"testing"
)

// The two viewers ADR 0112 was written from, run as tests. They are the whole
// argument for coupling the subtitle mode to the audio outcome: the same person,
// the same preference, two releases, two different right answers.

func TestAdamGetsForcedSubtitlesWithHisDubAndFullWithout(t *testing.T) {
	adam := LanguagePreference{
		Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesForced,
	}

	// The anime has an English dub, so he hears what he asked for and the forced
	// track translates the signage.
	withDub := adam.SubtitlesFor("eng")
	if withDub.Mode != SubtitlesForced || withDub.Escalated {
		t.Errorf("with an English dub: mode=%q escalated=%v, want forced and not escalated", withDub.Mode, withDub.Escalated)
	}

	// The same anime with no dub. He is now reading dialogue he cannot hear, so
	// forced subtitles would be three lines of signage over two hours of
	// Japanese — which is the case a subtitle setting on its own gets wrong.
	noDub := adam.SubtitlesFor("jpn")
	if noDub.Mode != SubtitlesFull {
		t.Errorf("without a dub: mode=%q, want full — forced subtitles over a language he does not speak communicate nothing", noDub.Mode)
	}
	if !noDub.Escalated {
		t.Error("the escalation is not recorded, so nothing can tell him why the subtitles changed")
	}
	if !reflect.DeepEqual(noDub.Languages, []string{"eng"}) {
		t.Errorf("languages = %v, want English — escalation changes how much is shown, never which language", noDub.Languages)
	}
}

func TestMaddieKeepsHerSubtitlesEitherWay(t *testing.T) {
	maddie := LanguagePreference{
		Audio: []string{"spa"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesFull,
	}

	// A Spanish dub exists, so her preference is met — and she still wants the
	// English transcript, which is why a subtitle mode is a preference rather
	// than a fallback.
	met := maddie.SubtitlesFor("spa")
	if met.Mode != SubtitlesFull || met.Escalated {
		t.Errorf("with a Spanish dub: mode=%q escalated=%v, want full and not escalated", met.Mode, met.Escalated)
	}

	// No Spanish. She was already asking for everything, so the escalation has
	// nothing to add and must not claim it did.
	missed := maddie.SubtitlesFor("jpn")
	if missed.Mode != SubtitlesFull || missed.Escalated {
		t.Errorf("without a Spanish dub: mode=%q escalated=%v, want full and still not escalated", missed.Mode, missed.Escalated)
	}
}

// TestOffIsNeverEscalatedPast is the bound that keeps escalation from becoming a
// second opinion about what someone wants. Escalation exists because the
// Platform failed to honour a preference; answering that failure by overriding a
// different preference would be the same mistake twice.
func TestOffIsNeverEscalatedPast(t *testing.T) {
	off := LanguagePreference{Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesOff}

	for _, spoken := range []string{"eng", "jpn", "spa", ""} {
		got := off.SubtitlesFor(spoken)
		if got.Mode != SubtitlesOff || got.Escalated {
			t.Errorf("audio %q: mode=%q escalated=%v, want off — someone who declined subtitles is not shown them because a dub was missing", spoken, got.Mode, got.Escalated)
		}
		if len(got.Languages) != 0 {
			t.Errorf("audio %q: languages=%v, want none when subtitles are off", spoken, got.Languages)
		}
	}
}

// TestAnUntaggedTrackDoesNotEscalate covers the case that would otherwise put
// full subtitles across most of a library. A single-audio release routinely
// carries no language tag, and absence of evidence is not evidence the viewer
// was given the wrong language.
func TestAnUntaggedTrackDoesNotEscalate(t *testing.T) {
	adam := LanguagePreference{Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesForced}

	for _, untagged := range []string{"", "und"} {
		got := adam.SubtitlesFor(untagged)
		if got.Mode != SubtitlesForced || got.Escalated {
			t.Errorf("audio %q: mode=%q escalated=%v, want the preference untouched", untagged, got.Mode, got.Escalated)
		}
	}
}

// TestAnUnreadablePreferenceIsTheDefaultNotAFailure pins the failure mode a
// preference should have. Costing a viewer their setting for one playback is
// recoverable; costing them the playback is not.
func TestAnUnreadablePreferenceIsTheDefaultNotAFailure(t *testing.T) {
	for _, raw := range [][]byte{nil, {}, []byte("not json"), []byte(`{"audio":"not-a-list"}`)} {
		got := ParseLanguagePreference(raw)
		if len(got.Audio) == 0 || got.SubtitleMode == "" {
			t.Errorf("ParseLanguagePreference(%q) = %+v, want a usable default", raw, got)
		}
	}
}

// TestStoredCodesAreComparedCaseInsensitively guards a silent failure: ffprobe
// reports lower-case, so a preference stored as "ENG" that is not normalised
// matches nothing and the viewer quietly gets whatever the release led with.
func TestStoredCodesAreComparedCaseInsensitively(t *testing.T) {
	got := ParseLanguagePreference([]byte(`{"audio":["ENG"," Spa "],"subtitles":["Eng"],"subtitleMode":"full"}`))

	if !reflect.DeepEqual(got.Audio, []string{"eng", "spa"}) {
		t.Errorf("Audio = %v, want lower-cased and trimmed", got.Audio)
	}
	if !got.wants("eng") || !got.wants("spa") {
		t.Error("a stored upper-case code does not match ffprobe's lower-case one")
	}
}

// TestAnUnknownModeFallsBackRatherThanRefusing covers a document written by a
// future client, or by hand. A mode nobody implements is not worth failing a
// play over.
func TestAnUnknownModeFallsBackRatherThanRefusing(t *testing.T) {
	got := ParseLanguagePreference([]byte(`{"audio":["eng"],"subtitleMode":"karaoke"}`))
	if got.SubtitleMode != DefaultLanguagePreference().SubtitleMode {
		t.Errorf("SubtitleMode = %q, want the default", got.SubtitleMode)
	}
}

// TestTheDefaultMatchesWhatTheInstallDidBefore is deliberate rather than
// incidental: introducing a preference must not change what anyone was already
// watching. A viewer's playback changes when they change their setting, not when
// this lands.
func TestTheDefaultMatchesWhatTheInstallDidBefore(t *testing.T) {
	if !reflect.DeepEqual(DefaultLanguagePreference().Audio, PreferredLanguages) {
		t.Errorf("default audio = %v, want the install-wide list it replaces (%v)",
			DefaultLanguagePreference().Audio, PreferredLanguages)
	}
}
