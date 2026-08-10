// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// InstanceIdentityStore persists what this install calls itself (platform#54).
//
// It is the one store deliberately **not** reached through Tx, and not because
// it was overlooked. Everything on Tx is there so that state and its outbox
// event commit together in PostgreSQL; this exists precisely so that a server's
// name survives PostgreSQL not being there. Putting it on the transaction would
// give it the durability guarantee of the thing it is insurance against.
//
// The consequence is stated rather than smoothed over: claiming a server writes
// two places that cannot commit together, and a crash between them leaves a
// claimed server with no name. That is the right way round — the account and
// its authority are the part that must be atomic, and a missing name is a
// setting somebody can fill in.
type InstanceIdentityStore interface {
	// Read returns this install's identity, or NotFound when nothing has been
	// written yet. NotFound rather than a zero value because "never claimed" and
	// "claimed and unnamed" are different answers and the doorway renders them
	// differently.
	Read(ctx context.Context) (domain.InstanceIdentity, error)
	// Write records the identity, replacing whatever was there.
	Write(ctx context.Context, identity domain.InstanceIdentity) error
}
