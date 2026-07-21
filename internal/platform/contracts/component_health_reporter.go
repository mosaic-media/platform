// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// ComponentHealthReporter reports a component's full diagnostics snapshot.
// Unlike HealthProbe.Check, it never returns an error: a reporter that cannot
// determine its own health reports that fact AS an Unavailable
// domain.ComponentHealth with a DegradedReason, rather than failing the
// caller. The diagnostics model keeps component health granular rather than
// reducing the whole system to a single failed state, so the reporting
// mechanism itself must not become a new way for the whole system to fail.
type ComponentHealthReporter interface {
	ReportHealth(ctx context.Context) domain.ComponentHealth
}
