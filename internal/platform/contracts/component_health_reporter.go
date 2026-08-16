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
// determine its own health reports that as an Unavailable
// domain.ComponentHealth with a DegradedReason, rather than failing the
// caller. Component health is kept granular rather than reduced to a single
// system-wide failed state, so the reporting mechanism itself must not become
// a new way for the whole system to fail.
type ComponentHealthReporter interface {
	ReportHealth(ctx context.Context) domain.ComponentHealth
}
