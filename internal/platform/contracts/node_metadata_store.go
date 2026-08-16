// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// NodeMetadata is what a metadata provider said about one node, and when.
//
// The document is stored opaquely — the Platform marshals v1.ContentMetadata
// into it and reads it back whole — so a field the SDK adds costs no
// migration. That is the same bet attributes and external_ids already take,
// made here for a document the Platform does interpret, which is why it is a
// Platform store rather than a module one (platform#62).
type NodeMetadata struct {
	NodeID v1.NodeID
	// Document is the marshalled descriptive metadata.
	Document []byte
	// Source is the module id that answered, so a document is attributable and a
	// provider that has since been replaced does not leave its answers reading
	// as the new one's.
	Source string
	// FetchedAt is when the provider was last asked. It is what "how stale is
	// this" is answered from, and what a refresh ordered by staleness sorts by.
	FetchedAt time.Time
}

// NodeMetadataStore persists the descriptive metadata of materialised content
// (platform#62).
//
// It is a cache in the sense that it can be rebuilt from its provider, and
// durable state in the sense that a library detail renders from it with no
// provider reachable at all. Both are true and the second is the point: an
// object graph whose screens need a third party to be up is not a library.
type NodeMetadataStore interface {
	// Upsert stores or replaces one node's document. Replacing rather than
	// merging: a document is one provider's whole answer at one moment, and
	// merging two answers would produce a description no source ever gave.
	Upsert(ctx context.Context, metadata NodeMetadata) (NodeMetadata, error)

	// Get reads one node's document. A node that has never been enriched is
	// NotFound, which a caller reads as "render what the node itself carries"
	// rather than as an error — an empty document and an absent one mean
	// different things about whether the provider was ever asked.
	Get(ctx context.Context, nodeID v1.NodeID) (NodeMetadata, error)
}
