// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// seedWorkWithItems seeds a Work and itemCount item children beneath it, with a
// Part attached to the child at partOn (-1 for none). The children are ordered,
// so partOn also says how far the walk has to go before it finds anything.
func seedWorkWithItems(db *fakeDB, itemCount, partOn int) v1.NodeID {
	const workID = v1.NodeID("work-1")
	db.seedNode(v1.Node{
		ID: workID, WorkID: workID, Kind: v1.NodeWork,
		MediaType: v1.MediaMovie, Title: "A Work",
	})
	for i := 0; i < itemCount; i++ {
		parent := workID
		childID := v1.NodeID("item-" + string(rune('a'+i)))
		db.seedNode(v1.Node{
			ID: childID, WorkID: workID, ParentID: &parent, Kind: v1.NodeItem,
			ItemType: v1.ItemFeature, MediaType: v1.MediaMovie,
			Title: "An Item", NaturalOrder: float64(i),
		})
		if i == partOn {
			db.seedPart(v1.Part{
				ID: v1.PartID("part-" + string(rune('a'+i))), NodeID: childID,
				Role:     v1.PartEdition,
				Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/media/a.mkv"},
			})
		}
	}
	return workID
}

// TestFirstPlayablePartClearsTheBoundaryOnce is the regression this whole
// change exists for, stated as a trace rather than a timing.
//
// The walk stops at the first item that has a Part, so a work whose playable
// child is last is the worst case — and it used to re-enter GetContentNode and
// ListNodeParts, paying a full authenticate-plus-authorize for the node read
// and then one more for every child it stepped over. Five children meant six
// session reads and six policy evaluations to learn one Part id.
//
// The trace pins the shape, not a number: one session read and one role read,
// wherever the Part turns out to be.
func TestFirstPlayablePartClearsTheBoundaryOnce(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	workID := seedWorkWithItems(db, 5, 4)
	svc := newTestService(db, tr, testNow)

	part, playable, err := svc.FirstPlayablePart(context.Background(),
		v1.Caller{Session: string(adminSession)}, workID)
	if err != nil {
		t.Fatalf("FirstPlayablePart() error = %v", err)
	}
	if !playable {
		t.Fatal("expected the seeded part to be found")
	}
	if part.ID != "part-e" {
		t.Fatalf("part.ID = %q, want %q", part.ID, "part-e")
	}

	// One authenticate, one authorize, one child listing, and one parts read
	// per child until the walk finds something. No re-entry: the boundary
	// appears exactly once, at the front.
	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"nodes.list_children",
		"parts.list_by_node",
		"parts.list_by_node",
		"parts.list_by_node",
		"parts.list_by_node",
		"parts.list_by_node",
	})
}

// TestFirstPlayablePartReportsNothingToPlay covers the honest negative: a work
// whose children carry no Parts is not an error, it is a work you cannot play
// yet, and the detail screen renders without a Play button (ADR 0036).
func TestFirstPlayablePartReportsNothingToPlay(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	workID := seedWorkWithItems(db, 3, -1)
	svc := newTestService(db, tr, testNow)

	_, playable, err := svc.FirstPlayablePart(context.Background(),
		v1.Caller{Session: string(adminSession)}, workID)
	if err != nil {
		t.Fatalf("FirstPlayablePart() error = %v", err)
	}
	if playable {
		t.Fatal("expected no playable part when no child has one")
	}
}

// TestFirstPlayablePartDoesNotDisguiseADenialAsNothingToPlay is the behaviour
// change that came with the fix.
//
// It used to swallow every error into false, so a caller who was not permitted
// to read content saw a detail screen with no Play button — indistinguishable
// from a work that genuinely has no bytes. Those are different facts and the
// screen has to be able to tell them apart.
func TestFirstPlayablePartDoesNotDisguiseADenialAsNothingToPlay(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	// A valid session with no role grants at all: the real policy engine
	// denies by default.
	db.seedSession("session-nobody", "user-nobody", testNow)
	workID := seedWorkWithItems(db, 1, 0)
	svc := newTestService(db, tr, testNow)

	_, playable, err := svc.FirstPlayablePart(context.Background(),
		v1.Caller{Session: "session-nobody"}, workID)
	if err == nil {
		t.Fatal("expected a denial, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}
	if playable {
		t.Fatal("a denied read must not report a playable part")
	}

	// The denial stopped at the boundary: no store was touched, and the
	// denial was audited.
	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"events.publish:authorization.denied",
	})
}

// TestFirstPlayablePartRejectsAnUnknownSession keeps the unauthenticated case
// beside the others rather than only in the conformance table, because this is
// the method whose old signature could not express it at all.
func TestFirstPlayablePartRejectsAnUnknownSession(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	workID := seedWorkWithItems(db, 1, 0)
	svc := newTestService(db, tr, testNow)

	_, _, err := svc.FirstPlayablePart(context.Background(),
		v1.Caller{Session: "does-not-exist"}, workID)
	if err == nil {
		t.Fatal("expected an error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

// TestFirstPlayablePartSkipsContainerChildren pins the deliberate limit: a
// series' children are seasons, not items, so nothing is playable at the work
// level and the screen offers Play per episode instead. Walking deeper would
// invent a default episode, which is the user's choice to make.
func TestFirstPlayablePartSkipsContainerChildren(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)

	const workID = v1.NodeID("series-1")
	parent := workID
	db.seedNode(v1.Node{
		ID: workID, WorkID: workID, Kind: v1.NodeWork,
		MediaType: v1.MediaTVSeries, Title: "A Series",
	})
	db.seedNode(v1.Node{
		ID: "season-1", WorkID: workID, ParentID: &parent, Kind: v1.NodeContainer,
		ContainerType: v1.ContainerSeason, MediaType: v1.MediaTVSeries, Title: "Season 1",
	})
	svc := newTestService(db, tr, testNow)

	_, playable, err := svc.FirstPlayablePart(context.Background(),
		v1.Caller{Session: string(adminSession)}, workID)
	if err != nil {
		t.Fatalf("FirstPlayablePart() error = %v", err)
	}
	if playable {
		t.Fatal("a series must report nothing playable at the work level")
	}

	// The container child was skipped without a parts read.
	assertTrace(t, tr, []string{
		"sessions.find_by_id",
		"permissions.roles_for_user",
		"nodes.list_children",
	})
}
