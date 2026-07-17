package runtime_test

import (
	"context"
	"sync"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// fakeReporter is a contracts.ComponentHealthReporter stand-in for
// diagnostics.Registry tests.
type fakeReporter struct{ health domain.ComponentHealth }

func (f fakeReporter) ReportHealth(context.Context) domain.ComponentHealth { return f.health }

// fakeConfigStore is a minimal in-memory contracts.ConfigStore — only
// FindActive is exercised by this package's tests, but every method must
// exist to satisfy the interface.
type fakeConfigStore struct {
	mu     sync.Mutex
	active *domain.ConfigVersion
}

func (s *fakeConfigStore) setActive(v domain.ConfigVersion) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.active = &v
}

func (s *fakeConfigStore) Save(_ context.Context, v domain.ConfigVersion) (domain.ConfigVersion, error) {
	return v, nil
}

func (s *fakeConfigStore) Latest(context.Context) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
}

func (s *fakeConfigStore) FindByID(context.Context, domain.ConfigVersionID) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
}

func (s *fakeConfigStore) FindActive(context.Context) (domain.ConfigVersion, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.active == nil {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no active config version")
	}
	return *s.active, nil
}

func (s *fakeConfigStore) UpdateStatus(_ context.Context, v domain.ConfigVersion) (domain.ConfigVersion, error) {
	return v, nil
}

// fakeOutbox is a minimal in-memory contracts.EventOutbox, enough to drive
// a real events.Worker in the Shutdown test.
type fakeOutbox struct {
	mu     sync.Mutex
	events map[domain.EventID]domain.OutboxEvent
}

func newFakeOutbox() *fakeOutbox {
	return &fakeOutbox{events: make(map[domain.EventID]domain.OutboxEvent)}
}

func (o *fakeOutbox) Append(_ context.Context, event domain.OutboxEvent) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events[event.ID] = event
	return nil
}

func (o *fakeOutbox) ListUnpublished(_ context.Context, limit int) ([]domain.OutboxEvent, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []domain.OutboxEvent
	for _, e := range o.events {
		if e.PublishedAt == nil && !e.DeadLettered {
			out = append(out, e)
			if len(out) == limit {
				break
			}
		}
	}
	return out, nil
}

func (o *fakeOutbox) MarkPublished(_ context.Context, id domain.EventID) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.events[id]
	now := time.Now().UTC()
	e.PublishedAt = &now
	o.events[id] = e
	return nil
}

func (o *fakeOutbox) RecordFailure(_ context.Context, id domain.EventID, category contracts.ErrorCategory, component string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	e := o.events[id]
	e.Attempts++
	e.LastErrorCategory = string(category)
	e.OwningComponent = component
	o.events[id] = e
	return nil
}

func (o *fakeOutbox) publishedCount() int {
	o.mu.Lock()
	defer o.mu.Unlock()
	n := 0
	for _, e := range o.events {
		if e.PublishedAt != nil {
			n++
		}
	}
	return n
}
