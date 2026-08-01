// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"encoding/json"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/transport/playback"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Preferences › Language — what a viewer wants to hear and read
// (ADR 0112).
//
// Under Preferences with Home, and for the same reason: it is taste. Four people
// share one library and disagree about this more than about anything else on the
// screen — which is why the Platform picking from a package variable was wrong
// rather than merely incomplete.
//
// Every control carries the whole document that pressing it produces, computed
// here where the current preference is already in hand. That is the settings
// screen's established shape (the home rows do the same) and it is what keeps
// `setPreference` a generic action that stores an uninterpreted value: the
// client echoes a decision rather than composing one.

// languageChoices are the languages offered, by the code ffprobe reports.
//
// A curated list rather than every ISO 639 code, because this is a control
// somebody reads: a list of several hundred is not a preference, it is a search
// problem. Nothing stops a stored document naming a code that is not here — the
// value is uninterpreted and the decision matches on the code — so this bounds
// what the *screen* offers and not what the Platform honours.
var languageChoices = []struct{ code, label string }{
	{"eng", "English"},
	{"jpn", "Japanese"},
	{"spa", "Spanish"},
	{"fra", "French"},
	{"deu", "German"},
	{"ita", "Italian"},
	{"por", "Portuguese"},
	{"kor", "Korean"},
	{"zho", "Chinese"},
}

// languagesPanel offers the audio language, the subtitle language and how much
// subtitle text to show.
func (s *Service) languagesPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel) (sdui.Node, error) {
	current := playback.ParseLanguagePreference(s.content.LanguagePreferenceFor(ctx, caller))

	audio := ui.Stack("vertical", 0,
		ui.Text(ui.Prop("text", "Audio"), ui.Prop("variant", "label")),
		ui.Text(ui.Prop("text", "The language to play when a release has it."), ui.Prop("variant", "caption")),
		languageRow(current, current.Audio, func(code string) playback.LanguagePreference {
			next := current
			next.Audio = []string{code}
			return next
		}))

	subtitles := ui.Stack("vertical", 0,
		ui.Text(ui.Prop("text", "Subtitles"), ui.Prop("variant", "label")),
		ui.Text(ui.Prop("text", "The language to read them in."), ui.Prop("variant", "caption")),
		languageRow(current, current.Subtitles, func(code string) playback.LanguagePreference {
			next := current
			next.Subtitles = []string{code}
			return next
		}))

	// The mode, and the sentence under it is the part worth reading. It is the
	// only place a viewer is told that what they pick here describes the case
	// where the Platform *could* give them the audio they asked for — and that
	// it will show more when it could not.
	modes := ui.Stack("horizontal", 2,
		modeButton(current, playback.SubtitlesOff, "Off"),
		modeButton(current, playback.SubtitlesForced, "Forced only"),
		modeButton(current, playback.SubtitlesFull, "Full"))

	show := ui.Stack("vertical", 0,
		ui.Text(ui.Prop("text", "Show subtitles"), ui.Prop("variant", "label")),
		ui.Text(ui.Prop("text", "What you want when you got the audio language you asked for. "+
			"When a release has no track in your language, forced subtitles become "+
			"full ones — so a dubbed film shows only the signage, and the same film "+
			"undubbed shows the whole dialogue."), ui.Prop("variant", "caption")),
		modes)

	// Typeset fidelity, and the sentence under it has to say what it costs.
	// Everything else on this screen is free; this one makes the server re-encode
	// the video, which on a weak machine is the difference between a release that
	// plays and one that does not. A setting that hid that would be a trap.
	styling := ui.Stack("vertical", 0,
		ui.Text(ui.Prop("text", "Styled subtitles"), ui.Prop("variant", "label")),
		ui.Text(ui.Prop("text", "Anime and other releases often place subtitles over the "+
			"picture — signs, captions, songs — in the colours and positions the author "+
			"chose. As authored sends the real thing and lets your player draw it, which "+
			"is free; a player that cannot falls back to plain text on its own. Burn into "+
			"video draws them in here instead, which works on any player and makes this "+
			"server re-encode a release it could otherwise pass through "+
			"untouched."), ui.Prop("variant", "caption")),
		ui.Stack("horizontal", 2,
			stylingButton(current, playback.StylingClient, "As authored"),
			stylingButton(current, playback.StylingPlain, "Plain text"),
			stylingButton(current, playback.StylingBurn, "Burn into video")))

	body := ui.Stack("vertical", 4, audio, subtitles, show, styling).Build()
	return settingsFrame(nav, sectionLanguages, "Language",
		"What you hear and what you read. Only ever yours — everyone sharing this "+
			"server chooses their own.", body), nil
}

// languageRow draws the offered languages, marking the one in force.
func languageRow(current playback.LanguagePreference, chosen []string, with func(string) playback.LanguagePreference) ui.El {
	selected := ""
	if len(chosen) > 0 {
		selected = chosen[0]
	}
	els := make([]ui.El, 0, len(languageChoices))
	for _, c := range languageChoices {
		tone := "ghost"
		if c.code == selected {
			tone = "secondary"
		}
		els = append(els, ui.Button(c.label, tone, ui.OnTap(setLanguages(with(c.code)))))
	}
	// Wrapped rather than a single row: nine languages do not fit a phone, and a
	// horizontal stack that overflows is a control a viewer cannot reach.
	return ui.Stack("horizontal", 2, els...)
}

// modeButton is one of the three subtitle modes, carrying the document it sets.
func modeButton(current playback.LanguagePreference, mode playback.SubtitleMode, label string) ui.El {
	tone := "ghost"
	if current.SubtitleMode == mode {
		tone = "secondary"
	}
	next := current
	next.SubtitleMode = mode
	return ui.Button(label, tone, ui.OnTap(setLanguages(next)))
}

// stylingButton is one of the three subtitle-styling choices, carrying the
// document it sets.
func stylingButton(current playback.LanguagePreference, styling playback.SubtitleStyling, label string) ui.El {
	tone := "ghost"
	if current.Styling == styling {
		tone = "secondary"
	}
	next := current
	next.Styling = styling
	// The field this replaced is cleared on any write, so a document written
	// before it existed stops carrying an answer that contradicts the new one
	// (ADR 0115). It is read on the way in and never written on the way out.
	next.Typeset = false
	return ui.Button(label, tone, ui.OnTap(setLanguages(next)))
}

// setLanguages is the action a control emits: this caller's own language
// preference, set to the document that control produces.
//
// It reuses `setPreference` rather than growing an action of its own, exactly as
// the home composition does. The store keeps the value uninterpreted and the
// playback decision owns its meaning, which is the same rule module settings
// follow (ADR 0021).
func setLanguages(p playback.LanguagePreference) ui.Action {
	// Marshalled here because the control has to carry a value, and a document
	// that will not encode is a control that would silently do nothing. An
	// encoding failure is not reachable for this shape — two string slices and a
	// string — so it degrades to the empty document, which reads back as the
	// default rather than as corruption.
	value := map[string]any{}
	if raw, err := json.Marshal(p); err == nil {
		_ = json.Unmarshal(raw, &value)
	}
	return ui.Invoke(setPreferenceMutation, map[string]any{
		"key":   domain.PreferenceLanguages,
		"value": value,
	})
}
