// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import (
	"encoding/json"
	"strings"
)

// What a viewer wants to hear and read (platform#83).
//
// The audio half is a list of languages, per viewer rather than per install. The
// coupling is the interesting half: a subtitle setting expressed on its own is
// wrong half the time, because the person who wants forced subtitles beside an
// English dub wants the whole dialogue when no dub exists, and that is one
// preference rather than two.
//
// So SubtitleMode says what they want when the audio preference was met, and the
// escalation below is the Platform noticing it was not.

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
	// Styling is what to do with a track that carries more than words —
	// positioned signs, colours, fonts (platform#83).
	Styling SubtitleStyling `json:"styling,omitempty"`

	// Typeset is the field Styling replaced, read for documents written before it
	// existed (platform#83). It meant "burn it in", which is what a true here still
	// resolves to.
	//
	// Kept rather than migrated because a preference document is written by
	// whichever client last touched it and read by every Platform after: a
	// migration would have to be a write, and a write to somebody's settings on
	// their behalf is a worse cost than a field that stays readable.
	Typeset bool `json:"typeset,omitempty"`
}

// SubtitleStyling is how much of a styled subtitle track a viewer wants, and
// therefore what the Platform has to spend to give it to them (platform#83).
type SubtitleStyling string

const (
	// StylingPlain flattens a styled track to plain text. It is free, and it
	// loses the positions, the colours and the sizes.
	StylingPlain SubtitleStyling = "plain"
	// StylingClient sends the track as authored and lets the client draw it.
	//
	// This is the default and it dominates the other two: it preserves
	// everything, it costs no encode, and a client that cannot draw it falls back
	// to the flattened rendition offered beside it. What it costs is a read of
	// the container to extract the script.
	StylingClient SubtitleStyling = "client"
	// StylingBurn draws the track into the picture. It works on every client
	// there is, including ones that can render nothing themselves, and it forces
	// a video encode and cannot be switched off mid-playback.
	StylingBurn SubtitleStyling = "burn"
)

// DefaultLanguagePreference is what a viewer who has set nothing gets.
//
// English audio with forced subtitles, the same answer the install-wide
// PreferredLanguages gives, so the preference changes nobody's playback until
// they change their own.
func DefaultLanguagePreference() LanguagePreference {
	return LanguagePreference{
		Audio:        []string{"eng", "en"},
		Subtitles:    []string{"eng", "en"},
		SubtitleMode: SubtitlesForced,
		// Styled tracks go to the client as authored, because that costs nothing
		// and degrades to the flattened rendition on a client that cannot draw
		// them. The expensive answer — burning — is the one somebody has to
		// choose (platform#83).
		Styling: StylingClient,
	}
}

// ParseLanguagePreference reads a stored preference document.
//
// Anything unreadable is the default rather than an error, which is the right
// failure for a preference: a malformed document costs a viewer their setting
// for one playback, never the playback itself. The home-row composition reads
// its own document on the same terms.
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
	out := LanguagePreference{SubtitleMode: p.SubtitleMode, Styling: p.Styling}
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
	switch out.Styling {
	case StylingPlain, StylingClient, StylingBurn:
	default:
		// A document written before Styling existed said typeset: true to mean
		// "burn it in", so that is what it still means. Anything else — an
		// unknown value, or nothing at all — takes the default.
		if p.Typeset {
			out.Styling = StylingBurn
		} else {
			out.Styling = DefaultLanguagePreference().Styling
		}
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
// got (platform#83).
//
// The escalation is the point. A viewer's mode describes a satisfied preference;
// when the release had no track in a language they asked for, the Platform knows
// it failed them and full subtitles are what that failure costs. One viewer
// watching an anime with an English dub gets English audio and forced subtitles;
// the same viewer, same preference, on a release with no dub, gets Japanese
// audio and the whole dialogue in English.
//
// Two bounds on it. Escalation only ever increases what is shown — there is no
// case where knowing less about the audio shows less text. And it never passes
// off: someone who said they want no subtitles is shown the release they can
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
