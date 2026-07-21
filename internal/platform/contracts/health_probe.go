// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// HealthProbe reports component readiness and degradation.
type HealthProbe interface {
	// Check reports the component's current health status.
	Check(ctx context.Context) (domain.HealthStatus, error)
}
