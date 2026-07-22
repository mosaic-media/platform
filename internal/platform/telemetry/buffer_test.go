// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// recordingWriter is a BatchWriter that remembers what it was given, and can
// be made to block or fail on demand.
type recordingWriter struct {
	mu      sync.Mutex
	records []telemetry.Record
	batches int

	block chan struct{}
	fail  bool
}

func (w *recordingWriter) WriteRecords(_ context.Context, records []telemetry.Record) error {
	if w.block != nil {
		<-w.block
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.fail {
		return errors.New("write failed")
	}
	w.batches++
	w.records = append(w.records, records...)
	return nil
}

func (w *recordingWriter) count() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.records)
}

func (w *recordingWriter) messages() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]string, 0, len(w.records))
	for _, r := range w.records {
		out = append(out, r.Message)
	}
	return out
}

func rec(msg string) telemetry.Record {
	return telemetry.Record{Time: time.Unix(0, 0), Level: telemetry.LevelInfo, Message: msg}
}

func TestBufferedSinkWritesThrough(t *testing.T) {
	w := &recordingWriter{}
	sink := telemetry.NewBufferedSink(w, 64, 4, 20*time.Millisecond)
	sink.Start(context.Background())

	for i := 0; i < 10; i++ {
		sink.Write(rec("record"))
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := w.count(); got != 10 {
		t.Fatalf("wrote %d records, want 10", got)
	}
	if sink.Dropped() != 0 {
		t.Fatalf("dropped %d with ample capacity", sink.Dropped())
	}
}

// TestBufferedSinkNeverBlocksTheCaller is the rule the whole design exists to
// enforce (ADR 0058): a user's playback must not wait on a log insert. The
// writer here is wedged, so every buffer slot fills and the only correct
// behaviour is for Write to keep returning promptly and shed load.
func TestBufferedSinkNeverBlocksTheCaller(t *testing.T) {
	w := &recordingWriter{block: make(chan struct{})}
	sink := telemetry.NewBufferedSink(w, 8, 4, time.Millisecond)
	sink.Start(context.Background())

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 5000; i++ {
			sink.Write(rec("under pressure"))
		}
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Write blocked while the writer was stalled; telemetry must shed load, never wait")
	}

	if sink.Dropped() == 0 {
		t.Fatal("expected records to be dropped once the buffer filled")
	}
	close(w.block)
	_ = sink.Close()
}

// TestBufferedSinkDropsOldestNotNewest pins the direction of the loss. Under
// sustained pressure the interesting records are the recent ones — whatever is
// happening now — so a buffer that discarded new arrivals would preserve a
// stale prefix of an incident while losing the incident itself.
func TestBufferedSinkDropsOldestNotNewest(t *testing.T) {
	w := &recordingWriter{block: make(chan struct{})}
	sink := telemetry.NewBufferedSink(w, 4, 64, time.Hour)
	// Not started: nothing drains, so the buffer is the only thing holding
	// records and the eviction order is observable without racing a drain.

	for i := 0; i < 3; i++ {
		sink.Write(rec("old"))
	}
	for i := 0; i < 8; i++ {
		sink.Write(rec("new"))
	}

	// Now drain: whatever survived is what the writer sees.
	close(w.block)
	w.block = nil
	sink.Start(context.Background())
	_ = sink.Close()

	for _, m := range w.messages() {
		if m == "old" {
			t.Fatalf("an old record survived while newer ones were written: %v", w.messages())
		}
	}
	if sink.Dropped() == 0 {
		t.Fatal("expected drops")
	}
}

// TestBufferedSinkFlushesOnClose guards the shutdown path: the last second of
// records is exactly the part describing why the process is stopping.
func TestBufferedSinkFlushesOnClose(t *testing.T) {
	w := &recordingWriter{}
	// A batch size and flush interval neither of which will trigger on their
	// own, so only the final flush can deliver these.
	sink := telemetry.NewBufferedSink(w, 64, 1000, time.Hour)
	sink.Start(context.Background())

	for i := 0; i < 7; i++ {
		sink.Write(rec("last words"))
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if got := w.count(); got != 7 {
		t.Fatalf("final flush delivered %d of 7 records", got)
	}
}

// TestBufferedSinkCountsWriteFailures covers the database being unavailable.
// The records are lost from the queryable sink and the file sink still has
// them — but the loss must be counted, or a sink failing silently is
// indistinguishable from a quiet system.
func TestBufferedSinkCountsWriteFailures(t *testing.T) {
	w := &recordingWriter{fail: true}
	sink := telemetry.NewBufferedSink(w, 64, 2, time.Millisecond)
	sink.Start(context.Background())

	for i := 0; i < 6; i++ {
		sink.Write(rec("into the void"))
	}
	_ = sink.Close()

	if sink.Failed() == 0 {
		t.Fatal("expected failed writes to be counted")
	}
	if sink.Dropped() != 0 {
		t.Fatalf("a write failure is not a buffer drop; dropped = %d", sink.Dropped())
	}
}

// TestBufferedSinkKeepsDrainingAfterItsContextIsCancelled is a regression test
// for a bug that only appeared when the real process shut down.
//
// The sink is started with the serve context, which is cancelled by the
// shutdown *signal*. Everything the composition root logs after that point —
// the signal itself, the final outbox drain, the shutdown health snapshot,
// "exiting cleanly" — is written afterwards. When cancellation stopped the
// drain loop, Close found it already gone and flushed nothing, so the entire
// account of the shutdown reached the file sink and never reached the
// queryable one. Silently, and only in production.
func TestBufferedSinkKeepsDrainingAfterItsContextIsCancelled(t *testing.T) {
	w := &recordingWriter{}
	sink := telemetry.NewBufferedSink(w, 64, 1000, time.Hour)

	ctx, cancel := context.WithCancel(context.Background())
	sink.Start(ctx)
	cancel()

	// The wait is the whole test, and it is why an earlier version of this
	// passed against the bug: a drain loop that watched ctx.Done() still
	// flushed whatever was already buffered on its way out, so writing
	// immediately after cancel() could not tell the two designs apart. The
	// production failure needed the loop to be *gone* before the last records
	// were written — which is what happens for real, since the shutdown
	// sequence takes seconds between the signal and "exiting cleanly".
	time.Sleep(250 * time.Millisecond)

	// Exactly the shape of the real shutdown path: log, then Close.
	for i := 0; i < 4; i++ {
		sink.Write(rec("shutting down"))
	}
	if err := sink.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if got := w.count(); got != 4 {
		t.Fatalf("records written after cancellation reached the writer %d/4 times", got)
	}
}

// TestBufferedSinkCloseIsIdempotent — the composition root closes it on the
// shutdown path, and a second call must not panic on a closed channel.
func TestBufferedSinkCloseIsIdempotent(t *testing.T) {
	sink := telemetry.NewBufferedSink(&recordingWriter{}, 8, 4, time.Millisecond)
	sink.Start(context.Background())
	_ = sink.Close()
	_ = sink.Close()
}
