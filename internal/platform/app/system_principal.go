// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"

	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The system principal — the case platform#13 named, reserved, and deliberately
// did not build.
//
// Background work has no session to forward. A scheduled sweep is not something
// a user asked for, so there is nobody to act as, and every command handler in
// this package begins by resolving a caller to a user. That left exactly two
// dishonest options: run background work as whichever administrator happened to
// be seeded (attributing the install's own maintenance to a person), or give
// the runner a back door around the boundary (a code path that mutates state
// without authorising, which is the thing platform#41 exists to make impossible).
//
// This is the third. The system principal is a **caller like any other**: it
// carries a session reference, it goes through `enter`, it is authorised by the
// policy engine, and every handler it reaches runs its ordinary boundary. What
// differs is only what the reference resolves to and what the engine says about
// it.
//
// Its authority is unbounded, which platform#13 says plainly and this does not
// soften. That is acceptable on platform#4's terms — the code that runs as it is
// compiled into this binary and trusted before the build — and it is why the
// reference is not a constant.

// SystemUserID is what the system principal authenticates as. It is not a user
// row and no `users` record can ever carry it: user ids are generated
// identifiers, so this literal is unreachable by CreateLocalUser.
//
// It exists so that an event, an attempt record or an audit line produced by
// background work has an actor that reads as what it is, rather than an empty
// string that could equally mean "nobody recorded this".
const SystemUserID domain.UserID = "mosaic.system"

// systemSessionRef is the opaque reference SystemCaller hands out, minted per
// process.
//
// **Random rather than a well-known string**, and that is the security
// property. A sentinel like "system" would be a session reference any client
// could put on the wire, and `Attach` would hand it unbounded authority — the
// boundary is honest, but the principal behind it would be forgeable. A value
// drawn from crypto/rand at construction is not: it never leaves the process,
// never reaches a client, and is not written anywhere.
//
// The cost is that it does not survive a restart, which costs nothing. Nothing
// persists a caller; a job stores its kind and its payload, and the runner is
// handed a fresh caller each time it runs one.
func newSystemSessionRef() string {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand failing means the process has no entropy source, which is
		// not a state to continue in with a predictable value. Panicking here
		// happens during construction, before anything is served.
		panic("mosaic: cannot mint the system principal: " + err.Error())
	}
	return "system:" + hex.EncodeToString(buf)
}

// SystemCaller returns the principal background work acts as (platform#13).
//
// It is exported because the composition root has to hand it to the jobs
// runner, and a job handler has to forward it to whatever service it calls —
// the same forwarding a module does with a user's caller. It is deliberately a
// method rather than a package function: minting one requires a Service, and a
// Service is what will authenticate it.
//
// **Anything holding one acts as the install with every permission.** Reach for
// it only where there is genuinely no user, which today means a job handler and
// the probe recording that runs beneath somebody's play without being theirs.
func (s *Service) SystemCaller() v1.Caller {
	return v1.Caller{Session: s.systemSession}
}

// isSystemCaller reports whether caller is this process's system principal.
//
// Compared in constant time. The value is not on the wire and a client cannot
// submit one that reaches here having been produced by guessing — but the
// comparison sits directly on the authentication path, and a length-and-prefix
// early exit on a secret is the shape of mistake that is obvious afterwards and
// invisible at review.
func (s *Service) isSystemCaller(caller v1.Caller) bool {
	if s.systemSession == "" || caller.Session == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(caller.Session), []byte(s.systemSession)) == 1
}

// principal is who a call acts as: a user id, and whether the Platform itself
// is the one acting.
//
// The flag is carried rather than derived from the id, so that "is this the
// system?" is answered by how the caller authenticated and never by a string
// comparison against SystemUserID. A value that can be recreated by writing a
// literal is a value an attacker can supply.
type principal struct {
	userID domain.UserID
	system bool
}

// resolvePrincipal is step 2 of the command boundary for either kind of
// caller. The system principal short-circuits the session read — there is no
// session row and there must not be one, because a session is issued to a
// device and revocable by a user, and neither is true of the install's own
// maintenance.
func (s *Service) resolvePrincipal(ctx context.Context, caller v1.Caller) (principal, error) {
	if s.isSystemCaller(caller) {
		return principal{userID: SystemUserID, system: true}, nil
	}
	userID, err := s.authenticate(ctx, domain.SessionCredential(caller.Session))
	if err != nil {
		return principal{}, err
	}
	return principal{userID: userID}, nil
}
