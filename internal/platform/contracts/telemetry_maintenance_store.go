// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"
	"time"
)

// PartitionRetention is how long each partitioned telemetry signal is kept.
//
// Two durations in a named struct rather than two parameters, because they are
// the same type and swapping them compiles: a call that transposed them would
// keep spans for a fortnight and logs for three days, and nothing but a bill
// would report it.
type PartitionRetention struct {
	Logs  time.Duration
	Spans time.Duration
}

// TelemetryMaintenanceStore is the write-side counterpart to
// TelemetryQueryStore: it creates the partitions records go into and drops the
// ones retention has run out on (platform#36).
//
// It exists as a contract, rather than the composition root reaching for the
// concrete PostgreSQL store, because retention is now a *job* — and a job
// handler calls an application service, which may depend on Platform contracts
// and never on a module's types. The interface is the seam that makes the
// dependency point inward.
type TelemetryMaintenanceStore interface {
	// EnsurePartitions creates the daily partitions covering [day, day+ahead).
	EnsurePartitions(ctx context.Context, day time.Time, ahead int) error
	// DropExpiredPartitions removes every partition wholly older than
	// retention, returning how many it dropped.
	DropExpiredPartitions(ctx context.Context, now time.Time, retention PartitionRetention) (int, error)
}
