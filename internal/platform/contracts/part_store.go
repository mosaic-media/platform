package contracts

import (
	"context"

	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// PartStore persists the bytes-bearing Parts of item nodes (ADR 0013).
//
// A Part points at bytes and never contains them (ADR 0014), so nothing
// here moves, copies or rewrites media.
type PartStore interface {
	// Create is InvalidArgument when the target node is not an item — a
	// work or container has nothing to play.
	Create(ctx context.Context, part v1.Part) (v1.Part, error)
	FindByID(ctx context.Context, id v1.PartID) (v1.Part, error)
	Update(ctx context.Context, part v1.Part) (v1.Part, error)

	// ListByNode returns every Part of an item ordered by NaturalOrder, so
	// the segments of a multi-disc edition come back in sequence. Editions
	// and segments share this one list because they share one
	// source-selection path.
	ListByNode(ctx context.Context, nodeID v1.NodeID) ([]v1.Part, error)

	Delete(ctx context.Context, id v1.PartID) error
}
