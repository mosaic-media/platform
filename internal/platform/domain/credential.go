// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// PasswordCredential is a local user's password verifier record. Hash is
// an opaque, already-hashed value (Argon2id in production); the Domain
// never sees or stores a plaintext password.
type PasswordCredential struct {
	UserID    UserID
	Hash      string
	UpdatedAt time.Time
}

// PasskeyCredential is one WebAuthn/platform passkey registered to a user.
// PublicKey is opaque to the Domain; verification belongs to a future
// crypto adapter.
type PasskeyCredential struct {
	UserID       UserID
	CredentialID string
	PublicKey    []byte
	CreatedAt    time.Time
}

// RecoveryFactor is one single-use account recovery code. Only its hash is
// held; ConsumedAt is set once the factor has been used, so a recovery
// ceremony invalidates the consumed code.
type RecoveryFactor struct {
	UserID     UserID
	CodeHash   string
	CreatedAt  time.Time
	ConsumedAt *time.Time
}

// Consumed reports whether this recovery factor has already been used.
func (f RecoveryFactor) Consumed() bool {
	return f.ConsumedAt != nil
}

// PasswordVerifier hashes and verifies password credentials. It is a
// Driven Port: the Domain needs only to hash and verify a credential, not a
// specific hashing algorithm, which belongs to a crypto adapter.
type PasswordVerifier interface {
	Hash(plaintext string) (string, error)
	Verify(plaintext string, hash string) (bool, error)
}

// TOTPCredential is one user's time-based one-time-password factor
// (platform#79).
//
// Despite the name it is a factor, not a credential: it verifies possession of
// a device and nothing more, because the server holds the same secret the
// authenticator does. It never resolves to a Principal on its own — it makes
// the password path two-step.
//
// Secret is the one piece of credential material the Platform cannot hash. A
// code is computed from it, so it must be recoverable, and anyone who reads it
// can mint valid codes forever. It is classified Secret wherever it travels
// (platform#34): never logged, never rendered after enrolment, never returned
// by a read a client can reach.
type TOTPCredential struct {
	UserID UserID
	// Secret is opaque to the Domain, exactly as PasswordCredential.Hash is.
	// What the string contains — a bare base32 secret or an encrypted
	// envelope — is the storage layer's business and not this type's.
	Secret string
	// Confirmed is false between generating a secret and the user proving they
	// scanned it. An unconfirmed factor must never gate a sign-in: storing one
	// as active on generation locks out anyone whose scan silently failed.
	Confirmed bool
	// LastUsedStep is the RFC 6238 counter of the most recently accepted code,
	// and it is what makes a replay refusable. A code stays valid for its whole
	// period, so without this the same six digits work again for up to a
	// minute. Verification requires the presented step to be strictly greater.
	LastUsedStep int64
	CreatedAt    time.Time
	ConfirmedAt  *time.Time
}

// Active reports whether this factor should be demanded at sign-in.
func (c TOTPCredential) Active() bool { return c.Confirmed }

// TOTPAuthenticator generates and verifies RFC 6238 codes. It is a Driven
// Port: the Domain needs a code checked, not a particular digest, period or
// digit count, which belong to a crypto adapter.
//
// Verify returns the counter step it matched so the caller can compare it with
// LastUsedStep. The adapter stores nothing and cannot enforce that itself.
type TOTPAuthenticator interface {
	GenerateSecret() (string, error)
	Verify(secret, code string, at time.Time) (step int64, ok bool, err error)
	ProvisioningURI(secret, issuer, account string) string
}
