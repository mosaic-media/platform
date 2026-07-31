// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import (
	"encoding/json"
	"strings"
)

// What a viewer wants to hear and read (ADR 0112).
//
// The audio half of this is only a list, and the Platform already ranked tracks
// against one — it was a package variable, so every viewer on an install got one
// person's answer. The interesting half is the coupling: **a subtitle setting
// expressed on its own is wrong half the time**, because the person who wants
// forced subtitles beside an English dub wants the whole dialogue when no dub
// exists, and that is one preference rather than two.
//
// So `SubtitleMode` says what they want *when the audio preference was met*, and
// the escalation below is the Platform noticing it was not.

// SubtitleMode is how much text a viewer wants when they got the audio language
// they asked for.
type SubtitleMode string

const (
	// SubtitlesOff shows none. It is never escalated past: someone who said they
	// want no subtitles is not given them because a dub was missing.
	SubtitlesOff SubtitleMode = "off"
	// SubtitlesForced shows only the tracks a release marks forced — the lines
	// that translate on-screen text and the occasional foreign phrase, rather
	// than a transcript of dialogue the viewer can hear.
	SubtitlesForced SubtitleMode = "forced"
	// SubtitlesFull shows the whole dialogue.
	SubtitlesFull SubtitleMode = "full"
)

// LanguagePreference is one viewer's answer, as stored under
// domain.PreferenceLanguages.
type LanguagePreference struct {
	// Audio is the languages to hear, most wanted first.
	Audio []string `json:"audio,omitempty"`
	// Subtitles is the languages to read, most wanted first.
	Subtitles []string `json:"subtitles,omitempty"`
	// SubtitleMode is what to show when the audio preference was met.
	SubtitleMode SubtitleMode `json:"subtitleMode,omitempty"`
	// Typeset asks for a styled subtitle track to be rendered as it was authored
	// — signs placed over the picture, colours, fonts — rather than flattened to
	// plain text (ADR 0114).
	//
	// **It is off by default and the reason is cost**, not taste. Honouring it
	// means burning the track into the picture, which forces a video encode on a
	// release that may not otherwise need one; the flattened rendition is free.
	// So this is opt-in, and the setting says what it costs.
	Typeset bool `json:"typeset,omitempty"`
}

// DefaultLanguagePreference is what a viewer who has set nothing gets.
//
// English audio with forced subtitles, which is the same answer the install-wide
// `PreferredLanguages` gave before this existed — deliberately, so adding the
// preference changes nobody's playback until they change their own. A default
// that silently altered what everyone already watched would be a worse
// introduction than no default at all.
func DefaultLanguagePreference() LanguagePreference {
	return LanguagePreference{
		Audio:        []string{"eng", "en"},
		Subtitles:    []string{"eng", "en"},
		SubtitleMode: SubtitlesForced,
	}
}

// ParseLanguagePreference reads a stored preference document.
//
// **Anything unreadable is the default rather than an error**, and that is the
// right failure for a preference: a malformed document should cost a viewer
// their setting for one playback, never the playback itself. The same reasoning
// the home-row composition uses for its own document.
func ParseLanguagePreference(raw []byte) LanguagePreference {
	if len(raw) == 0 {
		return DefaultLanguagePreference()
	}
	var p LanguagePreference
	if err := json.Unmarshal(raw, &p); err != nil {
		return DefaultLanguagePreference()
	}
	return p.normalised()
}

// normalised lower-cases the language codes and fills what was left out.
//
// Codes are compared against what ffprobe reports, which is lower-case, so a
// viewer who stored "ENG" must not silently stop matching anything.
func (p LanguagePreference) normalised() LanguagePreference {
	out := LanguagePreference{SubtitleMode: p.SubtitleMode, Typeset: p.Typeset}
	for _, l := range p.Audio {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			out.Audio = append(out.Audio, l)
		}
	}
	for _, l := range p.Subtitles {
		if l = strings.ToLower(strings.TrimSpace(l)); l != "" {
			out.Subtitles = append(out.Subtitles, l)
		}
	}
	if len(out.Audio) == 0 {
		out.Audio = DefaultLanguagePreference().Audio
	}
	switch out.SubtitleMode {
	case SubtitlesOff, SubtitlesForced, SubtitlesFull:
	default:
		// An unknown mode is the default rather than a refusal, for the same
		// reason an unreadable document is: a preference is not worth failing a
		// play over.
		out.SubtitleMode = DefaultLanguagePreference().SubtitleMode
	}
	return out
}

// wants reports whether language is one this viewer asked to hear.
func (p LanguagePreference) wants(language string) bool {
	language = strings.ToLower(language)
	for _, l := range p.Audio {
		if l == language {
			return true
		}
	}
	return false
}

// SubtitleIntent is what should be shown for one playback, after the release has
// had its say.
type SubtitleIntent struct {
	// Mode is the preference's mode, escalated if the audio preference was not
	// met.
	Mode SubtitleMode
	// Languages is the preferred subtitle languages, most wanted first. Empty
	// when Mode is off.
	Languages []string
	// Escalated records that this is not what the viewer asked for, so a caller
	// can say why full subtitles appeared on a release they normally watch with
	// forced ones.
	Escalated bool
}

// SubtitlesFor decides what a viewer should read, given the audio they actually
// got (ADR 0112).
//
// **The escalation is the feature.** A viewer's mode describes a satisfied
// preference; when the release had no track in a language they asked for, the
// Platform knows it failed them and full subtitles are what that failure costs.
// Adam watching an anime with an English dub gets English audio and forced
// subtitles; the same Adam, same preference, on a release with no dub, gets
// Japanese audio and the whole dialogue in English.
//
// Two bounds on it. Escalation only ever *increases* what is shown — there is no
// case where knowing less about the audio shows less text. And it never passes
// `off`: someone who said they want no subtitles is shown the release they can
// have, not text they declined.
//
// chosenAudioLanguage is empty when the release has no audio at all, which is
// not a failure to honour anything, so it does not escalate.
func (p LanguagePreference) SubtitlesFor(chosenAudioLanguage string) SubtitleIntent {
	intent := SubtitleIntent{Mode: p.SubtitleMode, Languages: p.Subtitles}
	if intent.Mode == SubtitlesOff {
		return SubtitleIntent{Mode: SubtitlesOff}
	}
	// An untagged track is not evidence the preference was missed. A
	// single-audio release routinely carries no language tag, and treating that
	// as a foreign language would put full subtitles on most of a library.
	if chosenAudioLanguage == "" || chosenAudioLanguage == "und" {
		return intent
	}
	if !p.wants(chosenAudioLanguage) && intent.Mode != SubtitlesFull {
		intent.Mode = SubtitlesFull
		intent.Escalated = true
	}
	return intent
}
