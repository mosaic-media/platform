// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"testing"
	"time"
)

// The standing-notice bookkeeping (platform#30).
//
// A source that is down fails on every render, and a source that recovers stops
// failing without announcing it. What the client is told has to be derived from
// the difference rather than from the event, which is what these cover.

// TestNoticeIsRaisedOnceAndRetractedByName is the whole lifetime. A repeat must
// not stack a fifth copy of the same warning, and a recovery must clear the
// exact notice it fixed rather than the stack.
func TestNoticeIsRaisedOnceAndRetractedByName(t *testing.T) {
	s := newLiveSession("s-1", time.Now())

	if !s.markNotice("source:tmdb") {
		t.Fatal("the first failure must raise a notice")
	}
	if s.markNotice("source:tmdb") {
		t.Fatal("a repeat must update the standing notice rather than raise a second one")
	}
	if !s.markNotice("source:cinemeta") {
		t.Fatal("a different source must raise its own notice")
	}

	// TMDB is answering again; Cinemeta still is not.
	stale := s.noticesExcept(sourceNoticePrefix, []string{"cinemeta"})
	if len(stale) != 1 || stale[0] != "source:tmdb" {
		t.Fatalf("to retract = %v, want only source:tmdb", stale)
	}

	s.clearNotice("source:tmdb")
	if !s.markNotice("source:tmdb") {
		t.Fatal("a source that fails again after recovering must raise its notice again")
	}
}

// TestNoticesExceptIgnoresOtherPrefixes guards the retraction from reaching
// notices this render knows nothing about. Source health is one condition among
// the several a standing notice will eventually carry, and clearing everything
// because one of them resolved would take away a warning nobody fixed.
func TestNoticesExceptIgnoresOtherPrefixes(t *testing.T) {
	s := newLiveSession("s-1", time.Now())
	s.markNotice("source:tmdb")
	s.markNotice("storage:full")

	stale := s.noticesExcept(sourceNoticePrefix, nil)
	if len(stale) != 1 || stale[0] != "source:tmdb" {
		t.Fatalf("to retract = %v, want only the source notice", stale)
	}
}

// TestNoticeMessagesCarryTheirLifetime is the wire shape. A notice the client
// must hold has to be both named and persistent — named so the server can
// retract it, persistent so the client does not remove it on a timer — and a
// retraction has to carry the name and nothing else.
func TestNoticeMessagesCarryTheirLifetime(t *testing.T) {
	raised := noticeMsg("source:tmdb", "tmdb is not responding", "warning").GetToast()
	if raised.GetId() != "source:tmdb" {
		t.Fatalf("id = %q, want the notice named", raised.GetId())
	}
	if !raised.GetPersistent() {
		t.Fatal("a standing notice must not expire on the client's timer: a lasting condition announced once is invisible")
	}
	if raised.GetCleared() {
		t.Fatal("a raised notice must not also retract itself")
	}

	cleared := clearedNoticeMsg("source:tmdb").GetToast()
	if !cleared.GetCleared() || cleared.GetId() != "source:tmdb" {
		t.Fatalf("retraction = %+v, want cleared and named", cleared)
	}
}

// TestSameRouteTreatsAbsentAndEmptyParamsAlike guards the comparison the push
// is gated on. A connect declares the home screen twice — once with no params
// and once with an empty object — so a strict comparison discards the
// revalidation of the screen the session is actually on, and the fresh result
// silently never arrives.
func TestSameRouteTreatsAbsentAndEmptyParamsAlike(t *testing.T) {
	if !sameRoute(route{screen: "home"}, route{screen: "home", params: map[string]any{}}) {
		t.Fatal("no params and empty params are the same route")
	}
	if !sameRoute(route{screen: "detail", params: map[string]any{"nodeId": "n-1"}},
		route{screen: "detail", params: map[string]any{"nodeId": "n-1"}}) {
		t.Fatal("equal params are the same route")
	}
	if sameRoute(route{screen: "detail", params: map[string]any{"nodeId": "n-1"}},
		route{screen: "detail", params: map[string]any{"nodeId": "n-2"}}) {
		t.Fatal("a different item is a different route: pushing over it would replace what the viewer opened")
	}
	if sameRoute(route{screen: "home"}, route{screen: "library"}) {
		t.Fatal("a different screen is a different route")
	}
}

// TestOneRevalidationPerSession bounds the background work. Without it a viewer
// tapping between two stale screens stacks a provider fan-out per tap, which is
// the cost this whole mechanism exists to keep off the render path.
func TestOneRevalidationPerSession(t *testing.T) {
	s := newLiveSession("s-1", time.Now())
	if !s.beginRevalidation() {
		t.Fatal("the first revalidation must claim the slot")
	}
	if s.beginRevalidation() {
		t.Fatal("a second revalidation must not start while one is running")
	}
	s.endRevalidation()
	if !s.beginRevalidation() {
		t.Fatal("the slot must be free again once the revalidation finishes")
	}
}
