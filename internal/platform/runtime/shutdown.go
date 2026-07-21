// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/events"
)

// ShutdownResult reports what graceful shutdown actually did — the graceful
// worker drain and outbox checkpoint.
type ShutdownResult struct {
	// FinalDrainPublished is how many outbox events the final drain
	// published before exit.
	FinalDrainPublished int
	// FinalDrainErr is the final drain's error, if the outbox mechanism
	// itself failed (not a single subscriber) during that last attempt.
	FinalDrainErr error
}

// Shutdown stops worker's background poll loop (if running) and then
// performs one final, synchronous drain, so any event that became
// deliverable between the last poll tick and Stop is not left behind —
// the outbox checkpoint shutdown requires. Worker.Stop already blocks
// until the poll goroutine has fully exited, so this final RunOnce never
// races a concurrent poll. lifecycle is marked Stopping before the drain
// and Stopped after, so Liveness reflects real shutdown progress
// throughout rather than flipping atomically at the very end.
func Shutdown(ctx context.Context, lifecycle *Lifecycle, worker *events.Worker) ShutdownResult {
	lifecycle.MarkStopping()
	worker.Stop()
	published, err := worker.RunOnce(ctx)
	lifecycle.MarkStopped()
	return ShutdownResult{FinalDrainPublished: published, FinalDrainErr: err}
}
