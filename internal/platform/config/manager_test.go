// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package config_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// sequentialIDGenerator produces deterministic, human-readable IDs.
type sequentialIDGenerator struct{ next int }

func (g *sequentialIDGenerator) NewID() domain.ID {
	g.next++
	return domain.ID(string(rune('a' - 1 + g.next)))
}

type fakeConfigStore struct {
	versions map[domain.ConfigVersionID]domain.ConfigVersion
}

func newFakeConfigStore() *fakeConfigStore {
	return &fakeConfigStore{versions: make(map[domain.ConfigVersionID]domain.ConfigVersion)}
}

func (s *fakeConfigStore) FindPending(context.Context) (domain.ConfigVersion, error) {
	for _, v := range s.versions {
		if v.Status == domain.ConfigPending {
			return v, nil
		}
	}
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version awaiting escalation")
}

func (s *fakeConfigStore) Save(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	s.versions[version.ID] = version
	return version, nil
}

func (s *fakeConfigStore) Latest(context.Context) (domain.ConfigVersion, error) {
	var latest domain.ConfigVersion
	found := false
	for _, v := range s.versions {
		if !found || v.CreatedAt.After(latest.CreatedAt) {
			latest, found = v, true
		}
	}
	if !found {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
	}
	return latest, nil
}

func (s *fakeConfigStore) FindByID(_ context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error) {
	v, ok := s.versions[id]
	if !ok {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
	}
	return v, nil
}

func (s *fakeConfigStore) FindActive(context.Context) (domain.ConfigVersion, error) {
	for _, v := range s.versions {
		if v.Status == domain.ConfigActive {
			return v, nil
		}
	}
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no active config version")
}

func (s *fakeConfigStore) UpdateStatus(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	if _, ok := s.versions[version.ID]; !ok {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
	}
	s.versions[version.ID] = version
	return version, nil
}

var testNow = time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

func testSchema(t *testing.T) *config.Schema {
	t.Helper()
	schema, err := config.NewSchema(
		config.FieldSpec{Name: "runtime.log_level", ReloadClass: config.Hot},
		config.FieldSpec{Name: "runtime.environment", ReloadClass: config.Restart},
		config.FieldSpec{Name: "composition.modules", ReloadClass: config.Generation},
		config.FieldSpec{Name: "storage.postgres.dsn", ReloadClass: config.Recovery},
		config.FieldSpec{Name: "storage.postgres.password", ReloadClass: config.Recovery, Secret: true},
	)
	if err != nil {
		t.Fatalf("NewSchema: %v", err)
	}
	return schema
}

func TestManagerDraftValidateActivateHotChange(t *testing.T) {
	store := newFakeConfigStore()
	ids := &sequentialIDGenerator{}
	manager := config.NewManager(fakeClock{now: testNow}, ids, testSchema(t))
	ctx := context.Background()

	draft, err := manager.Draft(ctx, store, []byte(`{"runtime.log_level":"debug"}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if draft.Status != domain.ConfigDraft {
		t.Fatalf("status = %q, want %q", draft.Status, domain.ConfigDraft)
	}

	validated, err := manager.Validate(ctx, store, draft.ID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != domain.ConfigValidated {
		t.Fatalf("status = %q, want %q", validated.Status, domain.ConfigValidated)
	}

	outcome, err := manager.Activate(ctx, store, draft.ID)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if !outcome.Activated || outcome.ReloadClass != config.Hot {
		t.Fatalf("outcome = %+v, want Activated=true ReloadClass=Hot", outcome)
	}
	if outcome.Version.Status != domain.ConfigActive {
		t.Fatalf("version status = %q, want %q", outcome.Version.Status, domain.ConfigActive)
	}
}

func TestManagerValidateRejectsUnregisteredField(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	draft, err := manager.Draft(ctx, store, []byte(`{"nonexistent.field":"x"}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	validated, err := manager.Validate(ctx, store, draft.ID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != domain.ConfigRejected {
		t.Fatalf("status = %q, want %q", validated.Status, domain.ConfigRejected)
	}

	_, err = manager.Activate(ctx, store, draft.ID)
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(Activate on rejected) = %s, want %s", got, contracts.Conflict)
	}
}

func TestManagerActivateDefersGenerationClassChange(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	draft, err := manager.Draft(ctx, store, []byte(`{"composition.modules":["postgres"]}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if _, err := manager.Validate(ctx, store, draft.ID); err != nil {
		t.Fatalf("Validate: %v", err)
	}

	outcome, err := manager.Activate(ctx, store, draft.ID)
	if err != nil {
		t.Fatalf("Activate: %v", err)
	}
	if outcome.Activated {
		t.Fatal("expected Activated = false for a Generation-class change")
	}
	if outcome.ReloadClass != config.Generation {
		t.Fatalf("ReloadClass = %q, want %q", outcome.ReloadClass, config.Generation)
	}

	stored, err := store.FindByID(ctx, draft.ID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	// Pending, not Validated: the request is recorded, which is what lets an
	// escalation tell a version somebody chose from one that merely passed
	// validation. It used to be left Validated, and a restart could not have
	// applied it without also applying every other validated candidate.
	if stored.Status != domain.ConfigPending {
		t.Fatalf("persisted status = %q, want %q", stored.Status, domain.ConfigPending)
	}
	if stored.RequestedAt == nil {
		t.Error("no RequestedAt — how long a change has been waiting is unanswerable")
	}
}

func TestManagerCannotValidateNonDraft(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	draft, err := manager.Draft(ctx, store, []byte(`{"runtime.log_level":"debug"}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if _, err := manager.Validate(ctx, store, draft.ID); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	// Validating an already-Validated version must be rejected, not silently
	// re-run.
	_, err = manager.Validate(ctx, store, draft.ID)
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Conflict)
	}
}

func TestManagerValidateAcceptsSecretFieldHoldingAReference(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	draft, err := manager.Draft(ctx, store, []byte(`{"storage.postgres.password":"secret://platform/postgres/password"}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	validated, err := manager.Validate(ctx, store, draft.ID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != domain.ConfigValidated {
		t.Fatalf("status = %q, want %q (detail: %s)", validated.Status, domain.ConfigValidated, validated.ValidationDetail)
	}
}

func TestManagerValidateRejectsSecretFieldHoldingARawValue(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	// Configuration should store secret references, not secret values: a
	// raw literal in a Secret field must never validate.
	draft, err := manager.Draft(ctx, store, []byte(`{"storage.postgres.password":"hunter2"}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	validated, err := manager.Validate(ctx, store, draft.ID)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if validated.Status != domain.ConfigRejected {
		t.Fatalf("status = %q, want %q for a raw secret value", validated.Status, domain.ConfigRejected)
	}
	if validated.ValidationDetail == "" {
		t.Fatal("expected a validation detail explaining the rejection")
	}
}

func TestManagerCannotActivateDraft(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	draft, err := manager.Draft(ctx, store, []byte(`{"runtime.log_level":"debug"}`))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	_, err = manager.Activate(ctx, store, draft.ID)
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Conflict)
	}
}

// requested drafts, validates and asks for a payload, returning its id.
func requested(t *testing.T, m *config.Manager, store contracts.ConfigStore, payload string) domain.ConfigVersionID {
	t.Helper()
	ctx := context.Background()
	draft, err := m.Draft(ctx, store, []byte(payload))
	if err != nil {
		t.Fatalf("Draft: %v", err)
	}
	if _, err := m.Validate(ctx, store, draft.ID); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if _, err := m.Activate(ctx, store, draft.ID); err != nil {
		t.Fatalf("Activate: %v", err)
	}
	return draft.ID
}

// The restart happens, and the change it was performed for is applied.
func TestApplyPendingAppliesARestartClassChange(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	id := requested(t, manager, store, `{"runtime.environment":"production"}`)

	outcome, err := manager.ApplyPending(ctx, store, config.Restart)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if !outcome.Activated {
		t.Fatal("the restart did not apply the change it was performed for")
	}
	stored, _ := store.FindByID(ctx, id)
	if !stored.Activated() {
		t.Errorf("status = %q, want active", stored.Status)
	}
	// Nothing left waiting, so the next boot does not apply it a second time.
	if _, err := store.FindPending(ctx); err == nil {
		t.Error("still pending after being applied — every boot would reapply it")
	}
}

// A restart must not carry a change that needs more than a restart. Applying
// a Generation-class change because a process happened to bounce is exactly
// what the classification exists to prevent.
func TestApplyPendingLeavesAChangeNeedingMoreThanWasGranted(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	id := requested(t, manager, store, `{"composition.modules":["postgres"]}`)

	outcome, err := manager.ApplyPending(ctx, store, config.Restart)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if outcome.Activated {
		t.Fatal("a restart applied a generation-class change")
	}
	if outcome.ReloadClass != config.Generation {
		t.Errorf("ReloadClass = %q, want generation", outcome.ReloadClass)
	}
	stored, _ := store.FindByID(ctx, id)
	if !stored.AwaitingEscalation() {
		t.Errorf("status = %q — it should still be waiting for the Supervisor", stored.Status)
	}
}

// Nothing waiting is the ordinary boot and must be silent, not an error every
// process start has to ignore.
func TestApplyPendingWithNothingWaitingIsSilent(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))

	outcome, err := manager.ApplyPending(context.Background(), store, config.Restart)
	if err != nil {
		t.Fatalf("ApplyPending: %v", err)
	}
	if outcome.Activated || outcome.Version.ID != "" {
		t.Errorf("reported something applied: %+v", outcome)
	}
}

// A Hot change is applied on the spot and never becomes pending, so a restart
// has nothing to do and cannot reapply it.
func TestAHotChangeNeverBecomesPending(t *testing.T) {
	store := newFakeConfigStore()
	manager := config.NewManager(fakeClock{now: testNow}, &sequentialIDGenerator{}, testSchema(t))
	ctx := context.Background()

	requested(t, manager, store, `{"runtime.log_level":"debug"}`)

	if _, err := store.FindPending(ctx); err == nil {
		t.Error("a hot change was recorded as awaiting an escalation")
	}
}
