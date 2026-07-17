package contracts

import "github.com/mosaic-media/mosaic-platform/internal/platform/domain"

// IDGenerator provides stable identity creation (MEG-015 §03). It commits
// to nothing about the generation strategy (UUID, ULID, sequence, ...);
// that choice belongs entirely to the adapter (MEG-004 §04 — Driven Ports).
type IDGenerator interface {
	NewID() domain.ID
}
