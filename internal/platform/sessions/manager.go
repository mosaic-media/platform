// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package sessions

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// DefaultLifetime is the session lifetime Manager.Issue applies. A
// configurable lifetime belongs to the Configuration slice; this is a
// fixed placeholder until then.
const DefaultLifetime = 24 * time.Hour

// Manager issues, validates and revokes sessions against whichever
// SessionStore a caller supplies. It takes the store as a parameter
// rather than holding one so application services can use it against
// both a direct, non-transactional SessionStore (for authentication reads)
// and a transaction-scoped one reached through Tx.Sessions() (for the
// write path) without needing two Manager instances.
type Manager struct {
	clock contracts.Clock
	ids   contracts.IDGenerator
}

// NewManager builds a Manager backed by clock and ids.
func NewManager(clock contracts.Clock, ids contracts.IDGenerator) *Manager {
	return &Manager{clock: clock, ids: ids}
}

// Issue creates and persists a new session for userID on deviceID at the
// given authentication strength.
func (m *Manager) Issue(ctx context.Context, store contracts.SessionStore, userID domain.UserID, deviceID domain.DeviceID, strength domain.AuthStrength) (domain.Session, error) {
	now := m.clock.Now()
	session := domain.Session{
		ID:           domain.SessionID(m.ids.NewID()),
		UserID:       userID,
		DeviceID:     deviceID,
		IssuedAt:     now,
		LastSeenAt:   now,
		ExpiresAt:    now.Add(DefaultLifetime),
		AuthStrength: strength,
	}
	return store.Create(ctx, session)
}

// Validate resolves sessionID and confirms it is neither revoked nor
// expired. Every SessionStore failure and every revoked/expired session
// is translated into the Unauthenticated category, so callers never need
// to branch on NotFound versus revocation versus expiry themselves,
// per the seven error categories.
func (m *Manager) Validate(ctx context.Context, store contracts.SessionStore, sessionID domain.SessionID) (domain.Session, error) {
	if sessionID == "" {
		return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "missing caller session")
	}

	session, err := store.FindByID(ctx, sessionID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return domain.Session{}, contracts.WrapError(contracts.Unauthenticated, "session not found", err)
		}
		return domain.Session{}, err
	}

	if session.Revoked() {
		return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "session revoked")
	}
	if session.ExpiredAt(m.clock.Now()) {
		return domain.Session{}, contracts.NewError(contracts.Unauthenticated, "session expired")
	}

	return session, nil
}

// Revoke revokes sessionID through store. Remote sign-out must go through
// this path rather than the client discarding its session handle,
// per the session model.
func (m *Manager) Revoke(ctx context.Context, store contracts.SessionStore, sessionID domain.SessionID) error {
	return store.Revoke(ctx, sessionID)
}
