// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Home composition, per user (platform#59).
//
// The library is one shared object graph and everything about how a person
// experiences it is theirs alone. Playback position was the first content state
// that differed between two users of one install; this is the first *browse*
// state that does.
//
// It is a preference and not a scope, and the distinction is the one
// platform#42 warns is a defect in opposite directions: a preference that gates is
// unenforceable, and a gate that is a preference is a security hole. Hiding a
// row is taste. The row stays reachable by search, by link and from every other
// surface.

// HomeComposition is the decisions one viewer made about their home screen.
//
// **Decisions, never a snapshot.** Hidden lists only the rows they turned off;
// Order lists only the rows they arranged. A row in neither is a row nobody has
// decided about, and it appears — which is what makes a newly available row
// show up for everybody rather than only for accounts created after it existed.
type HomeComposition struct {
	// Hidden are the rows this viewer turned off.
	Hidden []string
	// Order is the arrangement this viewer made, as the leading run of rows.
	//
	// A prefix rather than a total order, because a total order *is* a snapshot:
	// it would fix the position of rows the viewer never touched and leave a new
	// one with nowhere to go. Rows named here lead the screen in this sequence;
	// everything else follows in the server's own order.
	Order []string
}

// Hides reports whether this viewer turned a row off.
func (c HomeComposition) Hides(row string) bool {
	for _, h := range c.Hidden {
		if h == row {
			return true
		}
	}
	return false
}

// Arrange puts a set of row keys into this viewer's order: the rows they
// arranged, in their sequence, then everything else as the server offered it.
//
// It does not remove hidden rows. Ordering and visibility are separate
// decisions, and a settings panel has to show a hidden row in its place to offer
// it back — filtering here would mean the panel and the screen disagreed about
// where a row lives.
func (c HomeComposition) Arrange(rows []string) []string {
	// **Always a fresh slice, never the caller's.** Returning the input when
	// there is nothing to arrange reads like an obvious short-circuit and is a
	// trap: Swap arranges and then reorders in place, so the no-decision case
	// reordered the caller's own slice under it. The settings panel builds every
	// row's control from one key list, so drawing the first row's "Down" button
	// silently reversed the list the second row was read from — and every row
	// after the first drew the row above it. Compiled, passed, and was wrong on
	// sight in a browser.
	if len(c.Order) == 0 {
		return append([]string(nil), rows...)
	}
	offered := make(map[string]bool, len(rows))
	for _, r := range rows {
		offered[r] = true
	}
	out := make([]string, 0, len(rows))
	taken := make(map[string]bool, len(rows))
	// A row the viewer arranged that the server no longer offers is skipped
	// rather than dropped from the document: an addon that is temporarily down
	// should not silently erase the arrangement its rows were part of.
	for _, r := range c.Order {
		if offered[r] && !taken[r] {
			out = append(out, r)
			taken[r] = true
		}
	}
	for _, r := range rows {
		if !taken[r] {
			out = append(out, r)
		}
	}
	return out
}

// Swap moves a row one place in the arrangement and returns the composition
// that results — what the control offering that move carries as its argument.
//
// The stored Order becomes the leading run **up to and including** the pair that
// moved, so the screen keeps the shape the viewer was looking at when they
// pressed it. Recording only the two rows involved would be a smaller decision
// and a worse one: moving the bottom row up one place would send it and its
// neighbour to the top, which is not what the control appears to do.
func (c HomeComposition) Swap(rows []string, row string, up bool) HomeComposition {
	arranged := c.Arrange(rows)
	at := -1
	for i, r := range arranged {
		if r == row {
			at = i
			break
		}
	}
	to := at + 1
	if up {
		to = at - 1
	}
	if at < 0 || to < 0 || to >= len(arranged) {
		return c
	}
	arranged[at], arranged[to] = arranged[to], arranged[at]
	last := at
	if to > last {
		last = to
	}
	return HomeComposition{Hidden: c.Hidden, Order: append([]string(nil), arranged[:last+1]...)}
}

// Toggle returns the composition that results from turning a row on or off.
func (c HomeComposition) Toggle(row string, hidden bool) HomeComposition {
	out := HomeComposition{Order: c.Order}
	for _, h := range c.Hidden {
		if h != row {
			out.Hidden = append(out.Hidden, h)
		}
	}
	if hidden {
		out.Hidden = append(out.Hidden, row)
	}
	return out
}

// homeCompositionDoc is the stored shape. The JSON names are the wire the
// setPreference action carries, so they are written once here and read back by
// the same type rather than being spelled at a call site.
type homeCompositionDoc struct {
	Hidden []string `json:"hidden,omitempty"`
	Order  []string `json:"order,omitempty"`
}

// Document is this composition as the value a setPreference action carries.
//
// It is built server-side and handed to the control that would produce it, so
// the client echoes a decision rather than authoring one — the same rule every
// other action on every screen follows.
func (c HomeComposition) Document() map[string]any {
	doc := map[string]any{}
	if len(c.Hidden) > 0 {
		doc["hidden"] = c.Hidden
	}
	if len(c.Order) > 0 {
		doc["order"] = c.Order
	}
	return doc
}

// HomeCompositionFor reads how this viewer arranged their home screen.
//
// It authenticates and does not authorise, the same split ExpertModeEnabled
// takes and for the same reason (platform#36): a preference decides what a person
// is *shown*, and gating a display setting behind a second permission check is
// how a toggle becomes an accidental access control.
//
// It cannot fail. An unauthenticated caller, an unreadable store and a document
// this build cannot parse all yield the server's default composition, because a
// preference must never be able to fail a render — it only ever decides what to
// show, and a home screen that refused to draw because a taste setting could not
// be read would be a much worse outcome than one drawn in the default order.
func (s *Service) HomeCompositionFor(ctx context.Context, caller v1.Caller) HomeComposition {
	if s.userPreferences == nil {
		return HomeComposition{}
	}
	userID, err := s.authenticateCaller(ctx, caller)
	if err != nil {
		return HomeComposition{}
	}
	pref, err := s.userPreferences.Get(ctx, userID, domain.PreferenceHomeRows)
	if err != nil {
		return HomeComposition{}
	}
	var doc homeCompositionDoc
	if err := json.Unmarshal(pref.Value, &doc); err != nil {
		telemetry.From(ctx).For("screens").Warn("home composition could not be decoded; using the default",
			telemetry.Err(err))
		return HomeComposition{}
	}
	return HomeComposition{Hidden: doc.Hidden, Order: doc.Order}
}
