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

	// SaveTOTP writes a user's time-based factor, replacing any earlier one —
	// re-enrolling is how a user moves the factor to a new phone, and it must
	// not require a delete first.
	SaveTOTP(ctx context.Context, credential domain.TOTPCredential) error
	// FindTOTP reads a user's factor. NotFound is the ordinary answer: most
	// users have none.
	FindTOTP(ctx context.Context, userID domain.UserID) (domain.TOTPCredential, error)
	// DeleteTOTP removes it. Idempotent, so turning off a factor that is
	// already off is not an error a screen has to explain.
	DeleteTOTP(ctx context.Context, userID domain.UserID) error
	// ConsumeTOTPStep records that a counter step has been spent, and reports
	// whether it was still available.
	//
	// **The check and the write are one statement, and that is the whole
	// reason this is a store method rather than a read followed by a save.**
	// Two sign-ins racing with the same stolen code would both read the old
	// step and both write the new one, and the replay this exists to prevent
	// would succeed. A conditional update decides it in the database, where
	// only one can win.
	ConsumeTOTPStep(ctx context.Context, userID domain.UserID, step int64) (bool, error)

	SaveRecoveryFactor(ctx context.Context, factor domain.RecoveryFactor) error
	ConsumeRecoveryFactor(ctx context.Context, userID domain.UserID, codeHash string) (domain.RecoveryFactor, error)
}
