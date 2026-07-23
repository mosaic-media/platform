// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// PlaybackResolutionStore caches where a release's bytes are, per capability
// class (ADR 0049).
//
// It exists for one measurement: ranking is free, minting a ticket is free, and
// the aggregator call the answer comes from is not — an addon that fans out to
// many scrapers answers in hundreds of milliseconds to several seconds, and
// spending that between a click and a first frame is the entire latency budget
// on one call.
//
// It is deliberately *not* on Tx. Every other store there is joined to a
// transaction because its write must commit with an outbox event; this one has
// no event and nothing to be atomic with. More to the point, ADR 0049 requires
// the write not to block the stream — a user waiting on a database write in
// order to watch a film is the wrong trade in the one place latency was the
// whole point — so it is written after serving begins, outside any unit of work.
type PlaybackResolutionStore interface {
	// Get returns the cached resolution for a part and capability class, or
	// NotFound when that pair has never been resolved.
	//
	// A miss is the normal first answer for every newly added client class, and
	// it is not an error condition: it means the aggregator has to be asked,
	// which is what happened on every play before this existed.
	Get(ctx context.Context, partID, capabilityClass string) (domain.PlaybackResolution, error)
	// Set upserts one resolution.
	//
	// A failed link is overwritten rather than deleted, because the *candidate*
	// was never wrong — only its address changed. Deleting would throw away a
	// key that is about to be written again with a different value.
	Set(ctx context.Context, res domain.PlaybackResolution) error
	// Delete removes one entry. It is here for the case overwriting cannot
	// express: a re-resolve that produced nothing at all, where leaving the dead
	// URL in place would serve it again on the next play.
	Delete(ctx context.Context, partID, capabilityClass string) error
}
