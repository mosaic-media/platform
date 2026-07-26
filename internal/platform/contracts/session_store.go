// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// SessionStore provides session persistence and revocation.
type SessionStore interface {
	Create(ctx context.Context, session domain.Session) (domain.Session, error)
	FindByID(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Revoke(ctx context.Context, id domain.SessionID) error

	// Touch records that the session was used, moving LastSeenAt.
	//
	// It exists because idle expiry sits inside absolute expiry (ADR 0102),
	// and idle is measured from this column. It was written at issue and never
	// again, so a session that had been used every day for a month looked
	// exactly as idle as one nobody had touched since it was minted.
	Touch(ctx context.Context, id domain.SessionID, at time.Time) (domain.Session, error)

	// ListForUser returns a user's live sessions, newest first — the device
	// list ADR 0102 calls "the affordance that makes a bearer pair defensible
	// rather than merely convenient". Revoked and expired sessions are not
	// included: a list of devices somebody can no longer be signed in on is a
	// list of things they cannot act on.
	ListForUser(ctx context.Context, userID domain.UserID, now time.Time) ([]domain.Session, error)
}
