// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"context"
	"sync"
	"sync/atomic"
	"time"
)

// BatchWriter persists a batch of records. It is implemented by the storage
// module (internal/modules/postgres), which is why this is an interface here:
// the Platform tier decides what a record is and when it is written, and the
// module decides how. A writer must be safe for concurrent use, though
// BufferedSink calls it from one goroutine.
type BatchWriter interface {
	WriteRecords(ctx context.Context, records []Record) error
}

// Defaults sized for a self-hosted install: a few thousand records of headroom
// is seconds of burst at any plausible rate, and a second of latency to the
// queryable sink is invisible to someone reading a log viewer.
const (
	defaultCapacity  = 4096
	defaultBatchSize = 256
	defaultFlush     = time.Second
)

// BufferedSink is the queryable sink's front half: it accepts records without
// blocking and a background goroutine drains them to a BatchWriter.
//
// The rule it exists to enforce is that **telemetry never blocks a request**
// (ADR 0058). A user's playback must not wait on a log insert, and a telemetry
// subsystem able to stall the Platform is a liability rather than an asset. So
// Write never blocks and never returns an error; when the buffer is full it
// discards the *oldest* record and counts the loss.
//
// Dropping oldest rather than newest is deliberate. Under sustained pressure
// the interesting records are the recent ones — whatever is happening now —
// and a buffer that discards new arrivals would preserve a stale prefix of the
// incident while losing the incident.
type BufferedSink struct {
	records chan Record
	writer  BatchWriter

	batchSize int
	flush     time.Duration

	dropped atomic.Uint64
	failed  atomic.Uint64

	// done closes when the drain goroutine has exited, so Close can wait for
	// the final flush rather than racing it.
	done     chan struct{}
	stopOnce sync.Once
	stop     chan struct{}
}

// NewBufferedSink builds a sink over w. A zero capacity, batch size or flush
// interval takes the default.
func NewBufferedSink(w BatchWriter, capacity, batchSize int, flush time.Duration) *BufferedSink {
	if capacity <= 0 {
		capacity = defaultCapacity
	}
	if batchSize <= 0 {
		batchSize = defaultBatchSize
	}
	if flush <= 0 {
		flush = defaultFlush
	}
	return &BufferedSink{
		records:   make(chan Record, capacity),
		writer:    w,
		batchSize: batchSize,
		flush:     flush,
		done:      make(chan struct{}),
		stop:      make(chan struct{}),
	}
}

// Write enqueues r, discarding the oldest buffered record if the buffer is
// full. It never blocks and never fails.
func (b *BufferedSink) Write(r Record) {
	for {
		select {
		case b.records <- r:
			return
		default:
		}
		// Full: make room by discarding the oldest. The receive may lose to
		// the drain goroutine, in which case the loop simply retries with the
		// space the drain just freed — which is why this is a loop and not a
		// single attempt.
		select {
		case <-b.records:
			b.dropped.Add(1)
		default:
		}
	}
}

// Dropped is the number of records discarded for lack of buffer space. It is
// reported as telemetry in its own right: a subsystem silently losing records
// looks exactly like a quiet system, and the difference matters most during
// the incident that caused the pressure.
func (b *BufferedSink) Dropped() uint64 { return b.dropped.Load() }

// Failed is the number of records lost to write errors — a database that is
// down, a partition that does not exist. The file sink still has every one of
// them, which is the point of there being two.
func (b *BufferedSink) Failed() uint64 { return b.failed.Load() }

// Start runs the drain loop. It stops on Close, and deliberately **not** when
// ctx is cancelled.
//
// ctx is the parent for write contexts only. Making cancellation stop the loop
// is the obvious design and it is wrong: the composition root starts this with
// the serve context, which is cancelled by the shutdown *signal*, and the
// records emitted after that signal — "shutdown signal received", the final
// outbox drain, the shutdown health snapshot, "exiting cleanly" — are the ones
// most worth keeping. A loop that exited on cancellation would leave Close
// with nothing to flush and drop precisely the account of the shutdown.
//
// The consequence is that a caller must Close, or the goroutine outlives the
// sink. Every caller here does, on the shutdown path.
func (b *BufferedSink) Start(ctx context.Context) {
	go b.drain(ctx)
}

// Close stops the drain loop and waits for it to flush what it holds. It is
// safe to call more than once.
func (b *BufferedSink) Close() error {
	b.stopOnce.Do(func() { close(b.stop) })
	<-b.done
	return nil
}

// drain batches records by size or by time, whichever comes first.
func (b *BufferedSink) drain(ctx context.Context) {
	defer close(b.done)

	ticker := time.NewTicker(b.flush)
	defer ticker.Stop()

	batch := make([]Record, 0, b.batchSize)
	writeBatch := func() {
		if len(batch) == 0 {
			return
		}
		// A bounded context of its own: the parent may already be cancelled
		// (this is also the shutdown path), and a final flush that gives up
		// because shutdown began would throw away exactly the records
		// describing the shutdown.
		writeCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		if err := b.writer.WriteRecords(writeCtx, batch); err != nil {
			b.failed.Add(uint64(len(batch)))
		}
		cancel()
		batch = batch[:0]
	}

	for {
		select {
		case <-b.stop:
			b.finalFlush(&batch, writeBatch)
			return
		case r := <-b.records:
			batch = append(batch, r)
			if len(batch) >= b.batchSize {
				writeBatch()
			}
		case <-ticker.C:
			writeBatch()
		}
	}
}

// finalFlush drains whatever is still buffered before the loop exits, so a
// clean shutdown does not silently discard the last second of records.
func (b *BufferedSink) finalFlush(batch *[]Record, writeBatch func()) {
	for {
		select {
		case r := <-b.records:
			*batch = append(*batch, r)
			if len(*batch) >= b.batchSize {
				writeBatch()
			}
		default:
			writeBatch()
			return
		}
	}
}
