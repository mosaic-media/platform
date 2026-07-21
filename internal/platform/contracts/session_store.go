// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// SessionStore provides session persistence and revocation.
type SessionStore interface {
	Create(ctx context.Context, session domain.Session) (domain.Session, error)
	FindByID(ctx context.Context, id domain.SessionID) (domain.Session, error)
	Revoke(ctx context.Context, id domain.SessionID) error
}
