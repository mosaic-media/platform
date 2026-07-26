// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// InstanceIdentity is what one installation of Mosaic calls itself (ADR 0098).
//
// **It lives outside PostgreSQL, and that is the whole reason it is a type of
// its own rather than a row.** A household names its server during setup, and
// the name is then the only thing a person has to tell one Mosaic from another
// — on a device list, in a browser tab, and above all in the message a client
// shows when it cannot reach the Platform. A name that is only in the database
// is a name that disappears at exactly the moment somebody needs it, because
// "the database is down" and "I cannot tell you which server this is" are the
// same failure twice.
//
// So it is small and it is the only thing kept this way. Nothing else about an
// install belongs here: the moment a second field arrives that PostgreSQL could
// hold, it goes in PostgreSQL.
type InstanceIdentity struct {
	// ID identifies this install for as long as it exists, independently of its
	// name. A household that renames its server has not acquired a second one,
	// and a client that had pinned a name would think it had.
	ID string
	// Name is what the household called it.
	Name string
	// ClaimedAt is when somebody first took ownership. It is the one fact about
	// the claim recorded today: the audit record ADR 0098 asks for is a later
	// increment, and a timestamp with no actor and no address is not it.
	ClaimedAt time.Time
}

// Named reports whether this install has been given a name.
func (i InstanceIdentity) Named() bool { return i.Name != "" }
