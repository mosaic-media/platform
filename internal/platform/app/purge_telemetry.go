// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TelemetryPartitionsAhead is how many days of telemetry partitions a sweep
// keeps created in advance.
//
// The sweep is hourly, so the window only has to be far enough ahead that a
// sweep is safely early. It must never be short enough for a run to reach a
// midnight with nowhere to put its records.
const TelemetryPartitionsAhead = 3

// PurgeTelemetryCommand is one telemetry maintenance sweep.
type PurgeTelemetryCommand struct {
	Caller v1.Caller
}

// PurgeTelemetryResult is what the sweep did.
type PurgeTelemetryResult struct {
	// Dropped is how many daily partitions retention removed. Each is a day of
	// records for one signal, gone from the database rather than marked.
	Dropped int
	// Retention is the policy the sweep applied, read from the Active
	// configuration on this sweep rather than cached (platform#36: the retention
	// fields are Hot).
	Retention TelemetryRetention
}

// PurgeTelemetry extends the telemetry partition window and drops the
// partitions retention has run out on (platform#36).
//
// It is an ordinary application command — validate, authenticate, authorise,
// act — and not a privileged path the runner reaches around the boundary: the
// system principal authenticates like any other caller and is authorised like
// any other subject.
//
// It authorises telemetry.configure rather than a purge action of its own.
// Deleting under a policy an administrator set is that administrator exercising
// the policy, and a second action nothing else names would be a permission with
// one holder and one caller.
//
// The store beneath does DROP TABLE, not DELETE: that is what the partitioning
// is for. A day of records leaves as a catalogue update rather than a rewrite of
// a table somebody may be querying.
func (s *Service) PurgeTelemetry(ctx context.Context, cmd PurgeTelemetryCommand) (PurgeTelemetryResult, error) {
	if cmd.Caller.Session == "" {
		return PurgeTelemetryResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	if _, err := s.enter(ctx, cmd.Caller, ActionTelemetryConfigure, policy.Resource{Type: "telemetry"}); err != nil {
		return PurgeTelemetryResult{}, err
	}

	if s.telemetryMaintenance == nil {
		return PurgeTelemetryResult{}, contracts.NewError(contracts.Unavailable,
			"this Platform has no queryable telemetry sink to maintain")
	}

	now := s.clock.Now().UTC()
	retention := s.TelemetryRetention(ctx)

	// Extend first, drop second. The other order opens a window at a day
	// boundary in which the partition being written to has been dropped and its
	// replacement does not exist yet, and every insert in it fails.
	if err := s.telemetryMaintenance.EnsurePartitions(ctx, now, TelemetryPartitionsAhead); err != nil {
		return PurgeTelemetryResult{}, err
	}

	dropped, err := s.telemetryMaintenance.DropExpiredPartitions(ctx, now, contracts.PartitionRetention{
		Logs:  retention.Logs,
		Spans: retention.Spans,
	})
	if err != nil {
		return PurgeTelemetryResult{}, err
	}

	if dropped > 0 {
		telemetry.From(ctx).For("telemetry").Info("dropped expired telemetry partitions",
			telemetry.Int("partitions", dropped),
			telemetry.Duration("log_retention", retention.Logs),
			telemetry.Duration("span_retention", retention.Spans))
	}

	return PurgeTelemetryResult{Dropped: dropped, Retention: retention}, nil
}
