// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// InstanceIdentity is what one installation of Mosaic calls itself
// (platform#54).
//
// It is a type of its own rather than a row because it lives outside
// PostgreSQL. The name is what a person uses to tell one Mosaic from another,
// including in the message a client shows when it cannot reach the Platform —
// so a name held only in the database disappears at exactly the moment
// somebody needs it.
//
// It is deliberately the only thing kept this way. Anything else about an
// install that PostgreSQL could hold goes in PostgreSQL.
type InstanceIdentity struct {
	// ID identifies this install for as long as it exists, independently of its
	// name. A household that renames its server has not acquired a second one,
	// and a client that had pinned a name would think it had.
	ID string
	// Name is what the household called it.
	Name string
	// ClaimedAt is when somebody first took ownership. It is the one fact about
	// the claim recorded today: the audit record platform#54 asks for is a later
	// increment, and a timestamp with no actor and no address is not it.
	ClaimedAt time.Time
}

// Named reports whether this install has been given a name.
func (i InstanceIdentity) Named() bool { return i.Name != "" }
