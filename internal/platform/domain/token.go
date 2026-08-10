// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// The session credential (platform#58).
//
// A session used to be a fixed 24-hour lifetime with no renewal, and the id was
// the credential. It is now a **pair**: a minutes-long access token presented
// on every call, and a long-lived refresh token exchanged for a new pair. The
// session row survives both — it is what a device list names and what
// revocation ends — so the two are separate things and neither is the other.
//
// Both tokens are opaque, not JWTs, and the reason is not fashion: a
// claims-carrying token makes a tightened limit or a revoked grant take effect
// only when the token expires. Validation is a store read, and that cost is
// accepted deliberately.

// SessionCredential is the opaque value a caller presents — an access token
// since platform#58.
//
// It is its own type rather than a SessionID because it is no longer one. A
// credential resolves *to* a session, is minutes-lived where the session is
// months-lived, and rotates where the session does not; the two were one string
// for as long as they were one thing.
type SessionCredential string

// AccessToken is a short-lived credential, stored as a hash.
type AccessToken struct {
	// Hash is what is stored. The plaintext exists once, in the response that
	// issued it, and is never persisted anywhere.
	Hash      string
	SessionID SessionID
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// Expired reports whether the token is past its lifetime as of at.
func (t AccessToken) Expired(at time.Time) bool { return !at.Before(t.ExpiresAt) }

// RefreshToken is a long-lived, device-bound credential that is rotated on
// every use and chained to its predecessors.
type RefreshToken struct {
	Hash      string
	SessionID SessionID
	// ChainID is shared by every rotation of one original credential. Reuse
	// detection revokes the chain rather than the token, because by the time a
	// replay is seen, the thief and the legitimate client are both holding
	// descendants of the same original and there is no way to tell which is
	// which — so both are ended and the user signs in again.
	ChainID  ID
	DeviceID DeviceID
	IssuedAt time.Time
	// ExpiresAt is this token's own expiry. It never exceeds the session's
	// absolute expiry: a rotation cannot extend a session past the ceiling it
	// was issued under, or the absolute lifetime would be a lifetime only for
	// somebody who stopped using their account.
	ExpiresAt time.Time
	// UsedAt is when this token was exchanged. Non-nil means spent, and
	// presenting it again is the replay.
	UsedAt    *time.Time
	RevokedAt *time.Time
}

// Spent reports whether the token has already been exchanged.
func (t RefreshToken) Spent() bool { return t.UsedAt != nil }

// Revoked reports whether the token was explicitly revoked.
func (t RefreshToken) Revoked() bool { return t.RevokedAt != nil }

// Expired reports whether the token is past its lifetime as of at.
func (t RefreshToken) Expired(at time.Time) bool { return !at.Before(t.ExpiresAt) }

// Usable reports whether the token may be exchanged: not spent, not revoked,
// not expired.
func (t RefreshToken) Usable(at time.Time) bool {
	return !t.Spent() && !t.Revoked() && !t.Expired(at)
}

// TokenPair is what a client is handed: the plaintext of both tokens, and when
// each stops working.
//
// The plaintext exists only here, in the response that issues it. Nothing
// stores it, nothing logs it, and there is no way to read it back — which is
// what makes a database read of the token tables useless to whoever performed
// it.
type TokenPair struct {
	AccessToken      string
	AccessExpiresAt  time.Time
	RefreshToken     string
	RefreshExpiresAt time.Time
}
