// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package sessions

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The three lifetimes a session has since ADR 0102, and they are three
// different questions.
const (
	// AccessLifetime is how long a credential presented on an ordinary call
	// lasts. Minutes, because it is the window in which a stolen access token
	// still works, and because the whole reason validation is a store read
	// rather than a signature check is that a revocation should bite sooner
	// than an expiry.
	AccessLifetime = 10 * time.Minute

	// AbsoluteLifetime is how long a session can live however much it is used.
	// Past this, no refresh extends it and the user signs in again. It replaces
	// the fixed 24 hours a session used to have, which made "come back after a
	// fortnight and still be signed in" impossible by construction.
	AbsoluteLifetime = 90 * 24 * time.Hour

	// IdleLifetime is how long a session survives *unused*. It sits inside the
	// absolute lifetime, so a device somebody stopped using stops working long
	// before a device they use every day does — which is the difference
	// between a long-lived credential and an abandoned one.
	IdleLifetime = 30 * 24 * time.Hour
)

// DefaultLifetime is the absolute lifetime of a newly issued session. Kept as
// a name because callers and tests read it; it is AbsoluteLifetime now rather
// than the 24 hours it was.
const DefaultLifetime = AbsoluteLifetime

// tokenBytes is how much entropy each token carries. 32 bytes is past any
// brute-force argument, which is why the stored hash can be a plain SHA-256:
// there is no dictionary of 256-bit random values to run.
const tokenBytes = 32

// Manager issues, validates, refreshes and revokes sessions against whichever
// stores a caller supplies. It takes them as parameters rather than holding
// them so application services can use it against both a direct,
// non-transactional store (for authentication reads) and a transaction-scoped
// one reached through Tx (for the write path) without needing two instances.
type Manager struct {
	clock contracts.Clock
	ids   contracts.IDGenerator
}

// NewManager builds a Manager backed by clock and ids.
func NewManager(clock contracts.Clock, ids contracts.IDGenerator) *Manager {
	return &Manager{clock: clock, ids: ids}
}

// Issue creates a session and the first pair of tokens for it (ADR 0102).
//
// The session and its tokens are written through the stores the caller passes,
// which in the sign-in path are both transaction-scoped: a session with no
// tokens is a row nobody can use, and tokens with no session are a credential
// pointing at nothing.
func (m *Manager) Issue(
	ctx context.Context,
	sessions contracts.SessionStore,
	tokens contracts.TokenStore,
	userID domain.UserID,
	deviceID domain.DeviceID,
	strength domain.AuthStrength,
) (domain.Session, domain.TokenPair, error) {
	now := m.clock.Now()
	session := domain.Session{
		ID:           domain.SessionID(m.ids.NewID()),
		UserID:       userID,
		DeviceID:     deviceID,
		IssuedAt:     now,
		LastSeenAt:   now,
		ExpiresAt:    now.Add(AbsoluteLifetime),
		AuthStrength: strength,
	}
	stored, err := sessions.Create(ctx, session)
	if err != nil {
		return domain.Session{}, domain.TokenPair{}, err
	}

	pair, err := m.mint(ctx, tokens, stored, domain.ID(m.ids.NewID()), now)
	if err != nil {
		return domain.Session{}, domain.TokenPair{}, err
	}
	return stored, pair, nil
}

// Validate resolves a credential to the session behind it and confirms that
// session is usable.
//
// Every failure — an unknown token, an expired one, a revoked or expired
// session, an idle one — is the Unauthenticated category, so callers never
// branch on which. That is not laziness: the differences are exactly what an
// attacker would like told apart, and there is nothing a legitimate client does
// differently between them. It refreshes or it signs in.
func (m *Manager) Validate(
	ctx context.Context,
	sessions contracts.SessionStore,
	tokens contracts.TokenStore,
	credential domain.SessionCredential,
) (domain.Session, error) {
	if credential == "" {
		return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "missing caller credential")
	}
	if tokens == nil {
		return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "this Platform issues no session credentials")
	}

	now := m.clock.Now()
	token, err := tokens.FindAccess(ctx, HashToken(string(credential)))
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "invalid credential")
		}
		return domain.Session{}, err
	}
	if token.Expired(now) {
		return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "credential expired")
	}

	session, err := sessions.FindByID(ctx, token.SessionID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.Session{}, contracts.WrapError(contracts.Unauthenticated, "session not found", err)
		}
		return domain.Session{}, err
	}
	if err := m.usable(session, now); err != nil {
		return domain.Session{}, err
	}
	return session, nil
}

// Compromise is a refresh that revealed a stolen or replayed credential: the
// chain it belongs to must be revoked.
//
// It is returned rather than acted on, and that is not fastidiousness. The
// exchange runs inside a transaction — spending a token and storing its
// replacement have to commit together — so a revocation performed here would
// be rolled back by the very error that reported it, leaving the chain alive
// and the attacker working. The caller revokes outside the transaction and then
// returns the refusal.
//
// It was found by a test over the real wire, after the browser found the defect
// above it. A unit test with an in-memory store cannot see it: there is no
// rollback to undo the write.
type Compromise struct {
	ChainID domain.ID
	Reason  string
}

func (c Compromise) Error() string { return c.Reason }

// CompromiseOf reports whether err is a chain compromise, and which chain.
func CompromiseOf(err error) (Compromise, bool) {
	var c Compromise
	if errors.As(err, &c) {
		return c, true
	}
	return Compromise{}, false
}

// Refresh exchanges a refresh token for a new pair, rotating it.
//
// Rotation is the load-bearing part and reuse detection is what makes it worth
// having: a token presented twice revokes the whole chain, so theft becomes
// *detectable* rather than silent. The cost is stated rather than hidden — a
// stolen token used by an attacker and then by the legitimate user (or the
// reverse) signs that user out of that device. That is the intended behaviour.
func (m *Manager) Refresh(
	ctx context.Context,
	sessions contracts.SessionStore,
	tokens contracts.TokenStore,
	credential string,
	deviceID domain.DeviceID,
) (domain.Session, domain.TokenPair, error) {
	if credential == "" {
		return domain.Session{}, domain.TokenPair{}, contracts.NewError(contracts.Unauthenticated, "a refresh token is required")
	}

	now := m.clock.Now()
	token, err := tokens.FindRefresh(ctx, HashToken(credential))
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.Session{}, domain.TokenPair{}, contracts.NewError(contracts.Unauthenticated, "invalid refresh token")
		}
		return domain.Session{}, domain.TokenPair{}, err
	}

	// The device binding, checked before the token is spent. A refresh
	// presented from a device other than the one it was issued to is a stolen
	// credential being used, not a client that moved: a client cannot change
	// device without signing in, because the device id is what the session is
	// keyed to for revocation.
	if deviceID != "" && token.DeviceID != deviceID {
		return domain.Session{}, domain.TokenPair{}, Compromise{
			ChainID: token.ChainID,
			Reason:  "refresh token does not belong to this device",
		}
	}

	// Reuse detection. Spending is a conditional update, so a token that was
	// already spent — by a thief, by a racing tab, or by this client having
	// kept the old value — fails to spend here and takes the chain with it.
	spent, err := tokens.MarkRefreshUsed(ctx, token.Hash, now)
	if err != nil {
		return domain.Session{}, domain.TokenPair{}, err
	}
	if !spent {
		return domain.Session{}, domain.TokenPair{}, Compromise{
			ChainID: token.ChainID,
			Reason:  "this refresh token has already been used; the session has been ended on this device",
		}
	}
	if token.Revoked() || token.Expired(now) {
		return domain.Session{}, domain.TokenPair{}, contracts.NewError(contracts.Unauthenticated, "refresh token is no longer valid")
	}

	session, err := sessions.FindByID(ctx, token.SessionID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.Session{}, domain.TokenPair{}, contracts.WrapError(contracts.Unauthenticated, "session not found", err)
		}
		return domain.Session{}, domain.TokenPair{}, err
	}
	if err := m.usable(session, now); err != nil {
		return domain.Session{}, domain.TokenPair{}, err
	}

	// The refresh is itself use, so it moves the idle clock. Without this a
	// client that refreshes and then sits idle would be timed out on the
	// strength of when it last *navigated*, which is not what idle means.
	session.LastSeenAt = now
	if _, err := sessions.Touch(ctx, session.ID, now); err != nil {
		return domain.Session{}, domain.TokenPair{}, err
	}

	pair, err := m.mint(ctx, tokens, session, token.ChainID, now)
	if err != nil {
		return domain.Session{}, domain.TokenPair{}, err
	}
	return session, pair, nil
}

// Revoke ends a session and every credential behind it. Remote sign-out goes
// through this path rather than the client discarding what it holds — which
// since ADR 0102 means revoking the refresh chain, not merely dropping a value.
func (m *Manager) Revoke(ctx context.Context, sessions contracts.SessionStore, tokens contracts.TokenStore, sessionID domain.SessionID) error {
	if err := sessions.Revoke(ctx, sessionID); err != nil {
		return err
	}
	if tokens == nil {
		return nil
	}
	return tokens.RevokeSession(ctx, sessionID, m.clock.Now())
}

// usable is the session half of validation: revoked, past its absolute expiry,
// or idle for too long.
func (m *Manager) usable(session domain.Session, now time.Time) error {
	if session.Revoked() {
		return contracts.NewError(contracts.Unauthenticated, "session revoked")
	}
	if session.ExpiredAt(now) {
		return contracts.NewError(contracts.Unauthenticated, "session expired")
	}
	if !session.LastSeenAt.IsZero() && now.Sub(session.LastSeenAt) > IdleLifetime {
		return contracts.NewError(contracts.Unauthenticated, "session idle for too long")
	}
	return nil
}

// mint issues a fresh pair for a session, on the given chain.
//
// The refresh token's expiry is clamped to the session's absolute expiry, so a
// rotation cannot extend a session past the ceiling it was issued under. Without
// the clamp the absolute lifetime would only bind somebody who stopped using
// their account, which is precisely backwards.
func (m *Manager) mint(
	ctx context.Context,
	tokens contracts.TokenStore,
	session domain.Session,
	chainID domain.ID,
	now time.Time,
) (domain.TokenPair, error) {
	access, err := newToken()
	if err != nil {
		return domain.TokenPair{}, err
	}
	refresh, err := newToken()
	if err != nil {
		return domain.TokenPair{}, err
	}

	accessExpiry := now.Add(AccessLifetime)
	refreshExpiry := now.Add(AbsoluteLifetime)
	if refreshExpiry.After(session.ExpiresAt) {
		refreshExpiry = session.ExpiresAt
	}

	if err := tokens.SaveAccess(ctx, domain.AccessToken{
		Hash: HashToken(access), SessionID: session.ID, IssuedAt: now, ExpiresAt: accessExpiry,
	}); err != nil {
		return domain.TokenPair{}, err
	}
	if err := tokens.SaveRefresh(ctx, domain.RefreshToken{
		Hash: HashToken(refresh), SessionID: session.ID, ChainID: chainID,
		DeviceID: session.DeviceID, IssuedAt: now, ExpiresAt: refreshExpiry,
	}); err != nil {
		return domain.TokenPair{}, err
	}

	return domain.TokenPair{
		AccessToken:      access,
		AccessExpiresAt:  accessExpiry,
		RefreshToken:     refresh,
		RefreshExpiresAt: refreshExpiry,
	}, nil
}

// newToken draws a fresh opaque credential.
//
// crypto/rand, unpadded base64url so it survives a URL, a header and a JSON
// string without escaping. A failure here means the process has no entropy
// source, which is not a state to mint a credential in — so it is an error
// rather than a fallback to something weaker.
func newToken() (string, error) {
	buf := make([]byte, tokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", contracts.WrapError(contracts.Internal, "generate session token", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashToken is how a credential is stored and looked up.
//
// SHA-256 rather than a password KDF, deliberately. A token is 256 bits of
// randomness, so there is nothing to brute-force back from the hash; running
// Argon2 on the authentication path instead would put a deliberately expensive
// computation on every single call, which is a denial of service somebody
// eventually performs on themselves. What the hash buys is that a database
// read — a backup, a replica, a log — does not hand anybody a usable
// credential.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
