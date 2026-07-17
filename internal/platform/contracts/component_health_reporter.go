package contracts

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// ComponentHealthReporter reports a component's full diagnostics snapshot
// (MEG-015 §03, §09 — Diagnostics Model). Unlike HealthProbe.Check, it
// never returns an error: a reporter that cannot determine its own health
// reports that fact AS an Unavailable domain.ComponentHealth with a
// DegradedReason, rather than failing the caller — MEG-015 §09 requires
// component health to be "granular enough... without reducing the whole
// system to a single failed state", so the reporting mechanism itself must
// not be a new way for the whole system to fail.
type ComponentHealthReporter interface {
	ReportHealth(ctx context.Context) domain.ComponentHealth
}
