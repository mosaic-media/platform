package events

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

const (
	defaultBatchSize    = 50
	defaultPollInterval = time.Second
)

// Worker drains committed outbox rows and publishes them through an
// EventPublisher (MEG-015 §06 — First Event Model): Application service ->
// PostgreSQL transaction -> Outbox row -> Outbox worker -> In-process Event
// Bus -> Subscribers.
//
// On a successful Publish, the worker marks the event published. On a
// failed Publish, it records the failure through EventOutbox.RecordFailure,
// which applies the Platform delivery policy to schedule the next retry or
// dead-letter the event (MEG-015 §06 — Failure Behaviour) — a failed
// delivery is recorded and retried, never silently dropped.
//
// Worker also implements contracts.ComponentHealthReporter (MEG-015 §09 —
// Diagnostics Model): every RunOnce call updates real health bookkeeping —
// last successful check, last failure category, a degraded reason — rather
// than a hardcoded "ok".
type Worker struct {
	outbox    contracts.EventOutbox
	publisher contracts.EventPublisher
	component string
	clock     contracts.Clock

	batchSize    int
	pollInterval time.Duration

	stopCh chan struct{}
	doneCh chan struct{}

	healthMu            sync.Mutex
	lifecycle           domain.LifecycleState
	checked             bool
	lastState           domain.HealthState
	degradedReason      string
	lastSuccessfulCheck time.Time
	lastFailureCategory string
}

// Option configures a Worker.
type Option func(*Worker)

// WithBatchSize overrides the default number of events drained per poll.
func WithBatchSize(n int) Option {
	return func(w *Worker) { w.batchSize = n }
}

// WithPollInterval overrides the default interval between polls when
// running via Start.
func WithPollInterval(d time.Duration) Option {
	return func(w *Worker) { w.pollInterval = d }
}

// WithClock overrides the clock used for health bookkeeping timestamps.
// Tests use this for a deterministic LastSuccessfulCheck.
func WithClock(clock contracts.Clock) Option {
	return func(w *Worker) { w.clock = clock }
}

// NewWorker builds a Worker. component identifies this worker as the
// owning component recorded against failed deliveries (MEG-015 §06 —
// Failure Behaviour: "owning component") and reported as its own
// diagnostics identity (MEG-015 §09).
func NewWorker(outbox contracts.EventOutbox, publisher contracts.EventPublisher, component string, opts ...Option) *Worker {
	w := &Worker{
		outbox:       outbox,
		publisher:    publisher,
		component:    component,
		clock:        systemClock{},
		batchSize:    defaultBatchSize,
		pollInterval: defaultPollInterval,
		lifecycle:    domain.LifecycleStopped,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// RunOnce drains up to one batch of currently deliverable events (per
// EventOutbox.ListUnpublished — this excludes events still waiting out a
// retry backoff or already dead-lettered) and attempts to publish each. It
// returns the number of events successfully published.
//
// A single event's delivery failure does not stop the batch: that failure
// is recorded against just that event via RecordFailure, and every other
// event in the batch is still attempted. RunOnce only returns an error when
// the outbox itself is unusable (ListUnpublished, MarkPublished or
// RecordFailure failing outright) rather than when a subscriber handler
// fails — a subscriber failure is the expected, handled case this method
// exists to retry.
//
// RunOnce is the deterministic entry point for tests and for a single
// boot-time drain; Start uses it as the body of its poll loop. Every call
// updates the health bookkeeping ReportHealth reads.
func (w *Worker) RunOnce(ctx context.Context) (published int, err error) {
	events, err := w.outbox.ListUnpublished(ctx, w.batchSize)
	if err != nil {
		w.recordCheck(domain.HealthUnavailable, string(contracts.CategoryOf(err)), "list unpublished events failed")
		return 0, err
	}

	failures := 0
	var lastFailureCategory contracts.ErrorCategory
	for _, event := range events {
		if deliverErr := w.publisher.Publish(ctx, event.Event); deliverErr != nil {
			category := contracts.CategoryOf(deliverErr)
			lastFailureCategory = category
			failures++
			if recordErr := w.outbox.RecordFailure(ctx, event.ID, category, w.component); recordErr != nil {
				w.recordCheck(domain.HealthUnavailable, string(contracts.CategoryOf(recordErr)), "record delivery failure failed")
				return published, recordErr
			}
			continue
		}
		if markErr := w.outbox.MarkPublished(ctx, event.ID); markErr != nil {
			w.recordCheck(domain.HealthUnavailable, string(contracts.CategoryOf(markErr)), "mark event published failed")
			return published, markErr
		}
		published++
	}

	if failures > 0 {
		reason := fmt.Sprintf("%d of %d event(s) failed delivery this poll", failures, len(events))
		w.recordCheck(domain.HealthDegraded, string(lastFailureCategory), reason)
	} else {
		w.recordCheck(domain.HealthHealthy, "", "")
	}

	return published, nil
}

// recordCheck updates the health bookkeeping ReportHealth reads. It is the
// only place Worker's health state changes, so ReportHealth always reflects
// the outcome of the most recent RunOnce.
func (w *Worker) recordCheck(state domain.HealthState, failureCategory, reason string) {
	w.healthMu.Lock()
	defer w.healthMu.Unlock()

	w.checked = true
	w.lastState = state
	w.degradedReason = reason
	if failureCategory != "" {
		w.lastFailureCategory = failureCategory
	}
	if state == domain.HealthHealthy {
		w.lastSuccessfulCheck = w.clock.Now()
	}
}

// ReportHealth implements contracts.ComponentHealthReporter. Before the
// first RunOnce completes, it reports Unavailable — a worker that has never
// run cannot claim to be healthy.
func (w *Worker) ReportHealth(context.Context) domain.ComponentHealth {
	w.healthMu.Lock()
	defer w.healthMu.Unlock()

	health := domain.HealthUnavailable
	reason := "no delivery attempt has run yet"
	if w.checked {
		health = w.lastState
		reason = w.degradedReason
	}

	return domain.ComponentHealth{
		Component:           w.component,
		Lifecycle:           w.lifecycle,
		Health:              health,
		DegradedReason:      reason,
		LastSuccessfulCheck: w.lastSuccessfulCheck,
		LastFailureCategory: w.lastFailureCategory,
		// DegradedReason may echo internal error detail, so it defaults to
		// redacted in a support bundle rather than assumed safe.
		RedactionClass: domain.RedactionSensitive,
	}
}

// Start begins polling in a background goroutine every poll interval until
// ctx is cancelled or Stop is called. It returns immediately. Start must not
// be called again until a prior Start's Stop has returned.
func (w *Worker) Start(ctx context.Context) {
	w.healthMu.Lock()
	w.lifecycle = domain.LifecycleRunning
	w.healthMu.Unlock()

	w.stopCh = make(chan struct{})
	w.doneCh = make(chan struct{})
	go w.loop(ctx)
}

func (w *Worker) loop(ctx context.Context) {
	defer close(w.doneCh)

	ticker := time.NewTicker(w.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-w.stopCh:
			return
		case <-ticker.C:
			// Best-effort: an error here means the outbox itself failed
			// (not a single subscriber), which the next poll will retry.
			_, _ = w.RunOnce(ctx)
		}
	}
}

// Stop signals the poll loop to exit and waits for it to do so. Stop is a
// no-op if Start was never called.
func (w *Worker) Stop() {
	w.healthMu.Lock()
	w.lifecycle = domain.LifecycleStopped
	w.healthMu.Unlock()

	if w.stopCh == nil {
		return
	}
	close(w.stopCh)
	<-w.doneCh
}
