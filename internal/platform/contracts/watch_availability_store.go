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
// It is a projection of ContentMetadata.Watch, which the metadata enrichment
// pass already fetches (platform#62). The document that answer is stored in
// holds more — per-offer terms, deep links, the attribution TMDB's licence
// requires — and is read one screen at a time; this is the flat set a facet
// can be indexed on, written from the same fetch so the two cannot disagree
// about what a provider said.
type WatchAvailability struct {
	NodeID v1.NodeID
	// Region the answer is about. Availability is national and a substitute is a
	// wrong answer rather than a partial one, so it is stored rather than
	// inferred from configuration — a deployment that changes region must not
	// read its old answers as its new ones.
	Region string
	// Providers are the service names, as the provider named them.
	Providers []string
	// CheckedAt is when the provider was last asked. Availability churns
	// monthly, and a group saying "on Netflix" about a title that left in
	// March is worse than an absent group: a user can see a missing feature
	// and cannot see a lying one.
	CheckedAt time.Time
}

// WatchAvailabilityStore persists that projection.
//
// It is Platform-owned, unlike the module's own tmdbWatch attribute. That one
// is a module's document, stored uninterpreted (platform#9), and nothing here
// reads it: this is written from the typed value the SDK carries, so any
// metadata provider that fills Watch populates the facet and no module's key
// reaches the Platform.
type WatchAvailabilityStore interface {
	// Upsert stores or replaces one node's availability. Replacing rather than
	// merging: this is one provider's whole answer for one region at one moment,
	// and merging two would produce availability nobody reported.
	Upsert(ctx context.Context, availability WatchAvailability) (WatchAvailability, error)

	// ListStale returns the works most in need of a re-ask, up to limit.
	//
	// A work with no availability row yet comes first, which makes the refresh
	// a backfill on its first runs rather than only a maintenance pass. A
	// library that predates this store has an empty facet until something
	// asks, so a pass that only revisited rows it had already written would
	// keep that library permanently outside the feature.
	//
	// What it walks is the stored metadata, not the whole library: this is a
	// projection of a metadata answer, and a work that has never been enriched
	// has no ref to re-ask with (see the refresh's storedRefFor). A work enters
	// the rotation when it is first enriched and stays in it from then on.
	ListStale(ctx context.Context, limit int) ([]v1.NodeID, error)
}
