// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"sync"
	"time"
)

// What a render says about itself, beside the tree it returns (platform#30).
//
// A screen built from a durable snapshot has two things to tell the transport
// that a `UINode` cannot carry: that it should be revalidated, and that a source
// is not answering. The first schedules a background refresh whose result
// arrives as a `RegionUpdate`; the second raises a standing notice that has to
// be retracted by name when the source recovers.
//
// It rides the context rather than the return signature deliberately. Every
// screen builder returns `(sdui.Node, error)` and only the source-backed ones
// have anything to add — widening the signature would make every screen carry a
// value that thirteen of them always leave empty, and the one that forgot to
// would be indistinguishable from one with nothing to say.

type reportKey struct{}

// Report collects what a render learned about its sources. Its zero value is
// usable and means "nothing to report"; a nil *Report accepts writes and
// discards them, so a builder never has to check.
type Report struct {
	mu sync.Mutex
	// stale is set when the tree was built from a snapshot old enough to
	// revalidate. It is what schedules the background refresh.
	stale bool
	// snapshot is set when any part of the tree came from stored answers rather
	// than from the sources, whatever its age.
	snapshot bool
	// takenAt is the oldest stored answer the tree was built from — the age the
	// screen has to be able to state.
	takenAt time.Time
	// failed names the sources that did not answer this render.
	failed []string
}

// WithReport attaches a fresh Report to ctx and returns both. The transport
// calls it around a render; a builder never does.
func WithReport(ctx context.Context) (context.Context, *Report) {
	r := &Report{}
	return context.WithValue(ctx, reportKey{}, r), r
}

// reportFrom returns the Report ctx carries, or nil when a render was not asked
// for one — which is every render outside the session transport, including the
// tests that build a screen directly.
func reportFrom(ctx context.Context) *Report {
	r, _ := ctx.Value(reportKey{}).(*Report)
	return r
}

// note folds one browse answer's provenance into the report.
func (r *Report) note(fromSnapshot, stale bool, takenAt time.Time, failed []string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if fromSnapshot {
		r.snapshot = true
		if r.takenAt.IsZero() || (!takenAt.IsZero() && takenAt.Before(r.takenAt)) {
			r.takenAt = takenAt
		}
	}
	r.stale = r.stale || stale
	for _, f := range failed {
		if !contains(r.failed, f) {
			r.failed = append(r.failed, f)
		}
	}
}

// Stale reports whether this render should be revalidated in the background.
func (r *Report) Stale() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.stale
}

// FromSnapshot reports whether any of the tree came from stored answers.
func (r *Report) FromSnapshot() bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.snapshot
}

// TakenAt is the oldest stored answer the tree was built from, zero when none
// was.
func (r *Report) TakenAt() time.Time {
	if r == nil {
		return time.Time{}
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.takenAt
}

// Failed names the sources that did not answer this render.
func (r *Report) Failed() []string {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.failed...)
}

type refreshKey struct{}

// WithRefresh marks a render as a revalidation: every source-backed read on it
// skips the snapshot, asks its source, and stores what it says.
//
// It is the only way a stored answer is replaced, which is what makes the
// ordinary render cheap — a home screen a viewer returns to twice in a minute
// costs no provider round trip at all.
func WithRefresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, refreshKey{}, true)
}

// refreshing reports whether this render is a revalidation.
func refreshing(ctx context.Context) bool {
	on, _ := ctx.Value(refreshKey{}).(bool)
	return on
}

func contains(haystack []string, needle string) bool {
	for _, h := range haystack {
		if h == needle {
			return true
		}
	}
	return false
}
