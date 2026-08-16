// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Home composition, per user (platform#59).
//
// The property every one of these tests is the same: what is stored is the
// decisions a viewer made, so a row nobody has decided about appears rather than
// the home screen freezing at the shape it had when its owner first touched it.

// TestAnUndecidedRowAppears states that directly: a source that adds a catalog
// must show up for the viewer who already arranged the others.
func TestAnUndecidedRowAppears(t *testing.T) {
	// This viewer hid one row and moved another; they have never seen "new".
	c := app.HomeComposition{Hidden: []string{"b"}, Order: []string{"c", "a"}}

	got := c.Arrange([]string{"a", "b", "c", "new"})
	want := []string{"c", "a", "b", "new"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("arranged = %v, want %v — a row nobody decided about follows in the server's order", got, want)
	}
	if c.Hides("new") {
		t.Fatal("a row nobody decided about must not arrive hidden")
	}
}

// TestArrangeKeepsAnArrangementAcrossADownSource covers the interaction with
// cache-first rendering: a source that stops answering must not silently erase
// the arrangement its rows were part of, so a row that is not offered right now
// is skipped rather than dropped from the decision.
func TestArrangeKeepsAnArrangementAcrossADownSource(t *testing.T) {
	c := app.HomeComposition{Order: []string{"c", "a", "b"}}
	if got := c.Arrange([]string{"a", "b"}); strings.Join(got, ",") != "a,b" {
		t.Fatalf("arranged = %v, want a,b with the absent row skipped", got)
	}
	// And when it comes back it is where the viewer left it.
	if got := c.Arrange([]string{"a", "b", "c"}); strings.Join(got, ",") != "c,a,b" {
		t.Fatalf("arranged = %v, want the arrangement restored", got)
	}
}

// TestSwapPinsThePrefixItMoved is the ordering rule, and why it is a prefix
// rather than a pair: recording only the two rows involved sends a row moved up
// from the bottom straight to the top, taking its neighbour with it.
func TestSwapPinsThePrefixItMoved(t *testing.T) {
	rows := []string{"a", "b", "c", "d"}
	c := app.HomeComposition{}.Swap(rows, "d", true)

	if got := strings.Join(c.Order, ","); got != "a,b,d,c" {
		t.Fatalf("order = %v, want the leading run down to the pair that moved", c.Order)
	}
	if got := strings.Join(c.Arrange(rows), ","); got != "a,b,d,c" {
		t.Fatalf("arranged = %v, want d one place above c and nothing else moved", c.Arrange(rows))
	}
	// Still a decision and not a snapshot: a new row from the server appears.
	if got := strings.Join(c.Arrange([]string{"a", "b", "c", "d", "new"}), ","); got != "a,b,d,c,new" {
		t.Fatalf("arranged = %v, want the new row present", got)
	}
}

// TestArrangeDoesNotReorderItsCaller pins that Arrange returns a fresh slice
// even when there is nothing to arrange. Swap arranges and then reorders in
// place, and a settings panel draws every row's control from one shared key
// list, so returning the input reorders that list under the remaining rows.
func TestArrangeDoesNotReorderItsCaller(t *testing.T) {
	keys := []string{"continue", "a", "b"}
	var none app.HomeComposition

	none.Swap(keys, "continue", false)
	if strings.Join(keys, ",") != "continue,a,b" {
		t.Fatalf("caller's slice = %v, want it untouched by a call that only computes", keys)
	}

	decided := app.HomeComposition{Order: []string{"b"}}
	decided.Swap(keys, "a", true)
	if strings.Join(keys, ",") != "continue,a,b" {
		t.Fatalf("caller's slice = %v, want it untouched", keys)
	}
	if got := decided.Arrange(keys); &got[0] == &keys[0] {
		t.Fatal("Arrange returned the caller's own backing array; a later reorder would reach back into it")
	}
}

// TestSwapAtTheEndsIsANoOp guards the arithmetic the disabled controls rely on.
func TestSwapAtTheEndsIsANoOp(t *testing.T) {
	rows := []string{"a", "b"}
	var none app.HomeComposition
	if got := none.Swap(rows, "a", true); len(got.Order) != 0 {
		t.Fatalf("order = %v, want no decision recorded for a move that cannot happen", got.Order)
	}
	if got := none.Swap(rows, "b", false); len(got.Order) != 0 {
		t.Fatalf("order = %v, want no decision recorded", got.Order)
	}
	if got := none.Swap(rows, "nope", true); len(got.Order) != 0 {
		t.Fatalf("order = %v, want no decision for a row that is not there", got.Order)
	}
}

// TestToggleIsReversible proves hiding is a decision that can be taken back, and
// that taking it back leaves no trace — a document that accumulated every row a
// viewer had ever un-hidden would be a snapshot by another route.
func TestToggleIsReversible(t *testing.T) {
	c := app.HomeComposition{}.Toggle("a", true)
	if !c.Hides("a") {
		t.Fatal("toggling a row off must hide it")
	}
	c = c.Toggle("a", false)
	if c.Hides("a") || len(c.Hidden) != 0 {
		t.Fatalf("hidden = %v, want the decision removed rather than negated", c.Hidden)
	}
}

// TestHomeCompositionRoundTripsThroughThePreference is the whole path a control
// takes: the document a button carries, stored by setPreference, read back by
// the pass that builds home.
//
// The two halves are written in different places — the emit-side builds the
// document, the app service parses it — so a test that only exercised the
// structs would not notice them disagreeing about a field name.
func TestHomeCompositionRoundTripsThroughThePreference(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	svc := newTestService(db, &trace{}, now)
	db.seedUser(domain.User{ID: "u-1", Username: "viewer", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	caller := v1.Caller{Session: "s-1"}

	// The default is the server's, and expressing no preference means taking it.
	if got := svc.HomeCompositionFor(ctx, caller); len(got.Hidden) != 0 || len(got.Order) != 0 {
		t.Fatalf("default = %+v, want the server's composition", got)
	}

	decided := app.HomeComposition{}.Toggle("catalog:tmdb:movie:trending", true).
		Swap([]string{"continue", "catalog:tmdb:movie:popular"}, "catalog:tmdb:movie:popular", true)
	value, err := json.Marshal(decided.Document())
	if err != nil {
		t.Fatalf("marshal document: %v", err)
	}
	if _, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: caller, Key: domain.PreferenceHomeRows, Value: value,
	}); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}

	got := svc.HomeCompositionFor(ctx, caller)
	if !got.Hides("catalog:tmdb:movie:trending") {
		t.Fatalf("read back = %+v, want the hidden row preserved", got)
	}
	if strings.Join(got.Order, ",") != "catalog:tmdb:movie:popular,continue" {
		t.Fatalf("read back order = %v, want the arrangement preserved", got.Order)
	}
}

// TestHomeCompositionCannotFailARender is the fail-soft rule. A preference only
// ever decides what to show, so an unreadable one must yield the default
// rather than an error — a home screen that refused to draw because a taste
// setting could not be parsed is a much worse outcome than one drawn in the
// server's order.
func TestHomeCompositionCannotFailARender(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	svc := newTestService(db, &trace{}, now)
	db.seedUser(domain.User{ID: "u-1", Username: "viewer", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())

	// A document this build cannot read — an older or newer shape.
	if _, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: v1.Caller{Session: "s-1"}, Key: domain.PreferenceHomeRows, Value: []byte(`"not an object"`),
	}); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}
	if got := svc.HomeCompositionFor(ctx, v1.Caller{Session: "s-1"}); len(got.Hidden) != 0 || len(got.Order) != 0 {
		t.Fatalf("unreadable = %+v, want the default composition", got)
	}
	// And an unauthenticated caller gets the default rather than a panic.
	if got := svc.HomeCompositionFor(ctx, v1.Caller{Session: "nobody"}); len(got.Hidden) != 0 {
		t.Fatalf("unauthenticated = %+v, want the default composition", got)
	}
}
