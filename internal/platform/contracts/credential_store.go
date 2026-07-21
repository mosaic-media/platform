// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// CredentialStore provides local credential persistence and lookup for the
// local identity factors: password credentials, passkey credential records
// and local recovery factors.
type CredentialStore interface {
	SavePassword(ctx context.Context, credential domain.PasswordCredential) error
	FindPassword(ctx context.Context, userID domain.UserID) (domain.PasswordCredential, error)

	SavePasskey(ctx context.Context, credential domain.PasskeyCredential) error
	ListPasskeys(ctx context.Context, userID domain.UserID) ([]domain.PasskeyCredential, error)

	SaveRecoveryFactor(ctx context.Context, factor domain.RecoveryFactor) error
	ConsumeRecoveryFactor(ctx context.Context, userID domain.UserID, codeHash string) (domain.RecoveryFactor, error)
}
