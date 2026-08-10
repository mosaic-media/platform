// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Preferences › Home — where a viewer arranges their own home screen
// (ADR 0103).
//
// It sits under Preferences rather than under Server because it is taste: the
// library is one shared object graph and this decides nothing about it, only
// about how one person sees it. The library *rules* beside it are the opposite
// and live under Server, because everybody sees the same library and one person
// decides what goes in it.
//
// Every control carries the document that would result from pressing it,
// computed here where the row list is already in hand. The client echoes a
// decision rather than authoring one — the same rule every action on every
// screen follows — and it means the arithmetic of "what does moving this row up
// mean" lives beside the arithmetic that renders home from the answer.

// homeRowsPanel lists the rows this viewer's home can show, in the order it
// shows them, each with a switch and a pair of move controls.
func (s *Service) homeRowsPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel) (sdui.Node, error) {
	// The same read home makes, so the panel offers exactly the rows home would
	// draw. Cache-first (ADR 0052), so opening settings while a source is down
	// still lists that source's rows rather than quietly losing the arrangement
	// they are part of.
	cats, err := s.content.BrowseCatalogs(ctx, app.BrowseCatalogsQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	composition := s.content.HomeCompositionFor(ctx, caller)

	// Capability omission composes first (ADR 0036), which here means the panel
	// offers a row only if the caller could have it: the continue-watching rail
	// is listed because every signed-in viewer has playback state of their own,
	// and the catalog rows are whatever the installed sources actually offer. A
	// row nobody can reach is not offered to be hidden.
	type homeRow struct {
		key     string
		label   string
		summary string
	}
	rows := []homeRow{{
		key:     homeRowContinue,
		label:   "Continue watching",
		summary: "What you started and have not finished. Only ever yours.",
	}}
	seen := map[string]bool{homeRowContinue: true}
	for _, c := range cats.Catalogs {
		key := homeRowKey(c)
		if seen[key] {
			continue
		}
		seen[key] = true
		rows = append(rows, homeRow{key: key, label: c.Catalog.Name, summary: c.ModuleID})
	}

	keys := make([]string, 0, len(rows))
	byKey := make(map[string]homeRow, len(rows))
	for _, r := range rows {
		keys = append(keys, r.key)
		byKey[r.key] = r
	}
	// Arranged but **not** filtered: a hidden row keeps its place in this list so
	// it can be offered back. Hiding it here as well as on home would mean the
	// only way to restore a row is to remember it existed.
	arranged := composition.Arrange(keys)

	els := make([]ui.El, 0, len(arranged))
	for i, key := range arranged {
		row := byKey[key]
		hidden := composition.Hides(key)
		els = append(els, ui.SettingsRow(row.label,
			ui.Summary(row.summary),
			ui.Group(
				// Disabled rather than absent at the ends of the list. A control
				// that vanishes moves the ones beside it, so the button under the
				// pointer changes meaning between one press and the next.
				homeMoveButton("Up", composition, keys, key, true, i == 0),
				homeMoveButton("Down", composition, keys, key, false, i == len(arranged)-1),
				// The switch says its *state*, not the row's name. Repeating the
				// label beside the control it belongs to is noise on a row that
				// has already said it — and "Shown"/"Hidden" is the one thing a
				// switch cannot say for itself.
				//
				// **A finding rather than a choice:** `Switch` has no accessible
				// name in the contract (contracts#14), so a screen reader hears "on"
				// and nothing else whatever this text says — the Text beside it is
				// a sibling, not a label. Fixing that is a vocabulary change, not
				// a word here.
				ui.Toggle(homeVisibilityLabel(hidden),
					ui.On(!hidden),
					ui.OnTap(setHomeComposition(composition.Toggle(key, !hidden)))),
			)))
	}

	body := ui.Stack("vertical", 0, els...).Build()
	return settingsFrame(nav, sectionHomeRows, "Home",
		"Which rows your home screen shows, and in what order. Hiding a row is only "+
			"about your home screen — everything stays in search and in your library.",
		body), nil
}

// homeVisibilityLabel is what the switch beside a row says about it.
func homeVisibilityLabel(hidden bool) string {
	if hidden {
		return "Hidden"
	}
	return "Shown"
}

// homeMoveButton is one end of a row's ordering control, carrying the
// composition that results from pressing it.
//
// A worded button rather than a chevron pair, and that is a finding rather than
// a preference: the client's glyph set has `chevron-down` and no `chevron-up`,
// and the Icon primitive resolves an unknown name to *nothing* — so a
// hand-written "chevron-up" would have drawn an invisible control that still
// worked, which is the exact shape of a defect this project has shipped before.
// Adding the glyph is a client release, and it is not worth one for a settings
// row that reads perfectly well as Up and Down.
func homeMoveButton(label string, composition app.HomeComposition, keys []string, key string, up, atEnd bool) ui.El {
	if atEnd {
		return ui.Button(label, "ghost", ui.Disabled(true))
	}
	return ui.Button(label, "ghost",
		ui.OnTap(setHomeComposition(composition.Swap(keys, key, up))))
}

// setHomeComposition is the action a control emits: the caller's own home
// preference, set to the document that control produces.
//
// It reuses `setPreference` rather than growing an action of its own, and that
// is the honest shape: the value is a preference, the store keeps it
// uninterpreted, and the surface that reads it owns its meaning (ADR 0021's
// rule for module settings, which is the same rule). A `moveHomeRow` action
// would need the Platform to recompute the row list inside a command — a
// provider fan-out per press, to answer a question the screen that drew the
// button had already answered.
func setHomeComposition(composition app.HomeComposition) ui.Action {
	return ui.Invoke(setPreferenceMutation, map[string]any{
		"key":   domain.PreferenceHomeRows,
		"value": composition.Document(),
	})
}
