// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"reflect"
	"time"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/screens"
)

// Cache-first rendering's push half (platform#30).
//
// A source-backed screen is served from a durable snapshot and revalidated
// behind it; the live result arrives here, as a RegionUpdate on the push lane
// (contracts#5) that the client did not ask for.

// revalidateTimeout bounds a background revalidation. It fires after the intent
// that scheduled it has returned, so it cannot use that call's context and
// renders against a fresh bounded one instead.
//
// Generous, because a cold catalog call has been measured at 9.6 seconds and the
// whole point is that nobody is waiting on this one.
const revalidateTimeout = 45 * time.Second

// sourceNoticePrefix names the standing notice a failing source raises, so a
// repeat updates the notice already showing and a recovery retracts the exact
// one it fixed rather than clearing the stack.
const sourceNoticePrefix = "source:"

// applyReport turns what a render learned about its sources into what the client
// is told: a standing notice per failing source, retracted when it recovers, and
// a background revalidation when the tree was built from a stale snapshot.
//
// The notice state is per session rather than per process, and it has to be: a
// source's health is global, but "has this viewer been told" is not, and diffing
// against a global counter would show the notice to whichever session happened
// to render first and to nobody else.
func (h *Handler) applyReport(ctx context.Context, s *liveSession, rep *screens.Report, r route) {
	failing := rep.Failed()
	for _, source := range failing {
		if s.markNotice(sourceNoticePrefix + source) {
			// Persistent, not a toast. A toast is transient by design — right
			// for "import finished", wrong for a condition that is still true a
			// minute later, which is announced once and then invisible to
			// anybody who looked away.
			s.enqueue(noticeMsg(sourceNoticePrefix+source,
				sourceName(source)+" is not responding, so some rows may be out of date.",
				"warning"))
			telemetry.From(ctx).For("session").Warn("source unreachable; standing notice raised",
				telemetry.Identifier("session", s.ref), telemetry.String("source", source))
		}
	}
	for _, id := range s.noticesExcept(sourceNoticePrefix, failing) {
		s.clearNotice(id)
		s.enqueue(clearedNoticeMsg(id))
	}

	if rep.Stale() {
		h.revalidate(ctx, s, r)
	}
}

// revalidate re-renders the current route with every source-backed read forced
// live, and pushes the result into the content region.
//
// It runs in the requesting session's context (platform#30): there is a real
// caller behind it, so it needs no system principal and every read still
// authorises as the user who caused it. platform#13's reserved gap stays
// reserved.
func (h *Handler) revalidate(ctx context.Context, s *liveSession, r route) {
	if !s.beginRevalidation() {
		// One at a time per session. Without this, a viewer tapping between two
		// stale screens would stack a provider fan-out per tap.
		return
	}
	// The logger is carried over deliberately. A goroutine started from
	// context.Background() writes nothing, which would make the one thing this
	// slice must be able to prove — that a screen was served stale and then
	// refreshed — invisible in exactly the case worth looking at.
	lg := telemetry.From(ctx).For("session").With(
		telemetry.Identifier("session", s.ref), telemetry.String("screen", r.screen))
	caller := s.currentCaller()

	go func() {
		defer s.endRevalidation()
		ctx, cancel := context.WithTimeout(telemetry.Into(context.Background(), lg), revalidateTimeout)
		defer cancel()

		started := time.Now()
		ctx, rep := screens.WithReport(screens.WithRefresh(ctx))
		node, err := h.screens.Render(ctx, r.screen, caller, r.params)
		if err != nil {
			// A revalidation that fails changes nothing on screen: what is
			// already there is the snapshot, which is still the best answer
			// available. Replacing it with an error would take a working page
			// away to report that a refresh of it did not work.
			lg.Warn("revalidation failed; leaving the stored answer on screen",
				telemetry.Err(err), telemetry.Duration("elapsed", time.Since(started).Round(time.Millisecond)))
			return
		}

		// The route is re-read after the render because a fan-out takes seconds
		// and a viewer can navigate during it. Replacing the content region with
		// a home screen somebody has already left is worse than not refreshing
		// at all.
		if now := s.currentRoute(); !sameRoute(now, r) {
			lg.Info("revalidation discarded; the session moved on",
				telemetry.String("now", now.screen),
				telemetry.Duration("elapsed", time.Since(started).Round(time.Millisecond)))
			return
		}

		lg.Info("revalidation pushed as a region update",
			telemetry.Duration("elapsed", time.Since(started).Round(time.Millisecond)),
			telemetry.Int("failed_sources", len(rep.Failed())))
		s.enqueue(regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, node))

		// The refresh is also the honest place to raise or retract the standing
		// notice: it is the only read that actually asked the sources, so it is
		// the only one that knows whether they answered.
		h.applyReport(ctx, s, rep, r)
	}()
}

// sameRoute reports whether two routes show the same thing.
//
// An absent param map and an empty one are the same route, which is why this is
// not reflect.DeepEqual on the struct: a client that sends {} for a screen with
// no params and one that sends nothing produce map[string]any{} and nil
// respectively, and a connect does both. Comparing the structs discards a
// revalidation against the very screen it just rendered.
func sameRoute(a, b route) bool {
	if a.screen != b.screen || len(a.params) != len(b.params) {
		return false
	}
	for k, av := range a.params {
		bv, ok := b.params[k]
		if !ok || !reflect.DeepEqual(av, bv) {
			return false
		}
	}
	return true
}

// sourceName is what a notice calls a module. Its id, because that is what the
// extensions screen and the settings nav call it too, and inventing a display
// name here would be a second vocabulary for the same thing.
func sourceName(moduleID string) string { return moduleID }

// noticeMsg is a standing notice: the same stack a toast lands in, held until
// the user dismisses it or the Platform retracts it by name.
func noticeMsg(id, message, tone string) *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Toast{Toast: &sessionv1.Toast{
		Id: id, Message: message, Tone: tone, Persistent: true,
	}}}
}

// clearedNoticeMsg retracts the notice named by id — the condition resolved.
func clearedNoticeMsg(id string) *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Toast{Toast: &sessionv1.Toast{
		Id: id, Cleared: true,
	}}}
}
