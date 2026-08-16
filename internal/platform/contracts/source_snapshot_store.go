// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"
)

// SourceSnapshot is the last good answer a source gave to one question, kept so
// a screen built on that answer can be drawn again without asking (platform#30).
//
// It holds items, never a rendered tree. Artwork URLs are signed with a
// process-scoped key and playback tickets are sealed with another, both
// regenerated on boot, so a cached UINode tree comes back after a restart full
// of URLs signed by a key that no longer exists: the images fail while the page
// looks right. A snapshot of the items re-renders through the current key and
// is either correct or visibly stale.
type SourceSnapshot struct {
	// Source is the module that answered. Snapshots are keyed by it so a
	// provider that has been uninstalled and replaced cannot have its answers
	// read back as the new one's, and so failing sources can be named.
	Source string
	// Key is what was asked of that source, in the asker's own spelling — the
	// catalog's native type and id for a page of items, or the empty string for
	// "what catalogs do you have". It is opaque here: this store keeps answers
	// and does not interpret the questions.
	Key string
	// Document is the answer, marshalled. Opaque for the reason
	// node_metadata's is (platform#62): the shape is the SDK's, and a field the
	// SDK adds should cost no migration.
	Document []byte
	// TakenAt is when the source gave this answer. It is what "how old is this
	// screen" is answered from, and a screen served from a snapshot has to be
	// able to say — a two-day-old home beats an empty one, but only if nobody is
	// being told it is live.
	TakenAt time.Time
}

// SourceSnapshotStore persists the last good answer per source and question
// (platform#30).
//
// It lives in the Platform's own durable storage because the point is
// surviving a process restart, which an in-memory cache cannot; it also makes
// a source being down for an hour survivable rather than fatal.
//
// It is a cache in that every row can be rebuilt by asking the source again,
// and durable state in that a home screen renders from it with nothing
// reachable. The second is why it is a Platform store rather than something a
// module owns.
type SourceSnapshotStore interface {
	// Put stores or replaces one answer. Replacing rather than merging: a
	// snapshot is one source's whole answer at one moment, and merging two would
	// produce a page no source ever served.
	Put(ctx context.Context, snapshot SourceSnapshot) error

	// Get reads one answer. A question never asked is NotFound, which a caller
	// reads as "there is nothing to render from, so ask and wait" — the cold
	// install, which is the only render that should be slow.
	Get(ctx context.Context, source, key string) (SourceSnapshot, error)
}
