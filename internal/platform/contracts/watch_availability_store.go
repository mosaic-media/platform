// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// WatchAvailability is where one work could be watched, in one region, at one
// moment.
//
// It is a **projection** of `ContentMetadata.Watch`, which the metadata
// enrichment pass already fetches (ADR 0107). The document that answer is stored
// in holds more — per-offer terms, deep links, the attribution TMDB's licence
// requires — and is read one screen at a time; this is the flat set a facet can
// be indexed on, written from the same fetch so the two cannot disagree about
// what a provider said.
type WatchAvailability struct {
	NodeID v1.NodeID
	// Region the answer is about. Availability is national and a substitute is a
	// wrong answer rather than a partial one, so it is stored rather than
	// inferred from configuration — a deployment that changes region must not
	// read its old answers as its new ones.
	Region string
	// Providers are the service names, as the provider named them.
	Providers []string
	// CheckedAt is when the provider was last asked. This is the field the whole
	// slice turns on: **availability churns monthly**, and a group saying "on
	// Netflix" about a title that left in March is worse than an absent group,
	// because a user can see a missing feature and cannot see a lying one.
	CheckedAt time.Time
}

// WatchAvailabilityStore persists that projection.
//
// **Platform-owned, unlike the module's own `tmdbWatch` attribute.** That one is
// a module's document, stored uninterpreted (ADR 0013), and nothing here reads
// it — this is written from the typed value the SDK carries, so any metadata
// provider that fills `Watch` populates the facet and no module's key reaches
// the Platform.
type WatchAvailabilityStore interface {
	// Upsert stores or replaces one node's availability. Replacing rather than
	// merging: this is one provider's whole answer for one region at one moment,
	// and merging two would produce availability nobody reported.
	Upsert(ctx context.Context, availability WatchAvailability) (WatchAvailability, error)

	// ListStale returns the works most in need of a re-ask, up to limit.
	//
	// **A work with no availability row yet comes first**, and that is what makes
	// the refresh a backfill on its first runs rather than only a maintenance
	// pass. Every library that predates this store is in exactly that state — the
	// facet is empty until something asks — so a pass that only revisited rows it
	// had already written would keep an existing library permanently outside the
	// feature.
	//
	// What it walks is the **stored metadata**, not the whole library: this is a
	// projection of a metadata answer, and a work that has never been enriched
	// has no ref to re-ask with (see the refresh's storedRefFor). A work enters
	// the rotation when it is first enriched and stays in it from then on.
	ListStale(ctx context.Context, limit int) ([]v1.NodeID, error)
}
