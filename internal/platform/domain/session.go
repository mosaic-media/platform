// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// AuthStrength records which authentication factor produced a Session, so
// policy decisions can weigh session strength without depending on how the
// factor was verified.
type AuthStrength string

const (
	// AuthStrengthPassword marks a Session produced by password verification.
	AuthStrengthPassword AuthStrength = "password"
	// AuthStrengthPasswordTOTP marks a Session produced by a password *and* a
	// time-based second factor (ADR 0132).
	//
	// It is a distinct value rather than a flag beside AuthStrengthPassword
	// because the whole point of recording strength is that a policy can one
	// day read it: "this session proved two factors" is the fact worth
	// carrying, and a session that proved one must not be able to look like it.
	AuthStrengthPasswordTOTP AuthStrength = "password+totp"
	// AuthStrengthPasskey marks a Session produced by passkey verification.
	AuthStrengthPasskey AuthStrength = "passkey"
	// AuthStrengthRecovery marks a Session produced through the recovery flow.
	AuthStrengthRecovery AuthStrength = "recovery"
)

// Session is a server-issued, revocable Platform session. Fields match the
// session model's session table, plus RevokedAt to record revocation: remote
// sign-out revokes server-side session records rather than relying on clients
// deleting tokens.
type Session struct {
	ID           SessionID
	UserID       UserID
	DeviceID     DeviceID
	IssuedAt     time.Time
	LastSeenAt   time.Time
	ExpiresAt    time.Time
	AuthStrength AuthStrength
	Capabilities []Permission
	RevokedAt    *time.Time
}

// Revoked reports whether the session has been explicitly revoked.
func (s Session) Revoked() bool {
	return s.RevokedAt != nil
}

// ExpiredAt reports whether the session is expired as of at.
func (s Session) ExpiredAt(at time.Time) bool {
	return !at.Before(s.ExpiresAt)
}
