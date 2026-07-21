// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// --- DraftConfigVersion / ValidateConfigVersion / ActivateConfigVersion ---

func TestConfigVersionDraftValidateActivateHotChange(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)
	ctx := context.Background()

	drafted, err := svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: adminSession,
		Payload:         []byte(`{"runtime.log_level":"debug"}`),
	})
	if err != nil {
		t.Fatalf("DraftConfigVersion() error = %v", err)
	}
	if drafted.Version.Status != domain.ConfigDraft {
		t.Fatalf("status = %q, want %q", drafted.Version.Status, domain.ConfigDraft)
	}

	validated, err := svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{
		CallerSessionID: adminSession,
		ConfigVersionID: drafted.Version.ID,
	})
	if err != nil {
		t.Fatalf("ValidateConfigVersion() error = %v", err)
	}
	if validated.Version.Status != domain.ConfigValidated {
		t.Fatalf("status = %q, want %q", validated.Version.Status, domain.ConfigValidated)
	}

	activated, err := svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{
		CallerSessionID: adminSession,
		ConfigVersionID: drafted.Version.ID,
	})
	if err != nil {
		t.Fatalf("ActivateConfigVersion() error = %v", err)
	}
	if !activated.Activated {
		t.Fatalf("Activated = false, want true for a Hot-only change")
	}
	if activated.ReloadClass != config.Hot {
		t.Fatalf("ReloadClass = %q, want %q", activated.ReloadClass, config.Hot)
	}
	if activated.Version.Status != domain.ConfigActive {
		t.Fatalf("status = %q, want %q", activated.Version.Status, domain.ConfigActive)
	}

	db.mu.Lock()
	stored := db.configs[drafted.Version.ID]
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	db.mu.Unlock()
	if stored.Status != domain.ConfigActive {
		t.Fatalf("persisted status = %q, want %q", stored.Status, domain.ConfigActive)
	}

	wantEvents := map[string]bool{"config.drafted": false, "config.validated": false, "config.activated": false}
	for _, e := range outbox {
		if _, ok := wantEvents[e.Type]; ok {
			wantEvents[e.Type] = true
		}
	}
	for eventType, seen := range wantEvents {
		if !seen {
			t.Fatalf("expected outbox event %q, got %+v", eventType, outbox)
		}
	}
}

func TestValidateConfigVersionRejectsUnregisteredField(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)
	ctx := context.Background()

	drafted, err := svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: adminSession,
		Payload:         []byte(`{"totally.unknown.field":"x"}`),
	})
	if err != nil {
		t.Fatalf("DraftConfigVersion() error = %v", err)
	}

	validated, err := svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{
		CallerSessionID: adminSession,
		ConfigVersionID: drafted.Version.ID,
	})
	if err != nil {
		t.Fatalf("ValidateConfigVersion() error = %v", err)
	}
	if validated.Version.Status != domain.ConfigRejected {
		t.Fatalf("status = %q, want %q", validated.Version.Status, domain.ConfigRejected)
	}
	if validated.Version.ValidationDetail == "" {
		t.Fatal("expected a validation detail explaining the rejection")
	}

	// A rejected version can never be activated.
	_, err = svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{
		CallerSessionID: adminSession,
		ConfigVersionID: drafted.Version.ID,
	})
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Conflict)
	}
}

func TestActivateConfigVersionRejectsGenerationClassChangeFromHotApplying(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)
	ctx := context.Background()

	// composition.modules is Generation-classed (config.PlatformSchema):
	// changing which Modules are composed into the binary requires the
	// Supervisor to activate a new Generation, not a hot apply.
	drafted, err := svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: adminSession,
		Payload:         []byte(`{"composition.modules":["postgres","anime"]}`),
	})
	if err != nil {
		t.Fatalf("DraftConfigVersion() error = %v", err)
	}
	if _, err := svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{
		CallerSessionID: adminSession,
		ConfigVersionID: drafted.Version.ID,
	}); err != nil {
		t.Fatalf("ValidateConfigVersion() error = %v", err)
	}

	result, err := svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{
		CallerSessionID: adminSession,
		ConfigVersionID: drafted.Version.ID,
	})
	if err != nil {
		t.Fatalf("ActivateConfigVersion() error = %v", err)
	}
	if result.Activated {
		t.Fatal("expected Activated = false for a Generation-class change; must not fake-apply it as Hot")
	}
	if result.ReloadClass != config.Generation {
		t.Fatalf("ReloadClass = %q, want %q", result.ReloadClass, config.Generation)
	}

	// The version must be left Validated, not silently moved to Active or
	// any other state, so a future Supervisor-handoff slice can pick it up.
	db.mu.Lock()
	stored := db.configs[drafted.Version.ID]
	db.mu.Unlock()
	if stored.Status != domain.ConfigValidated {
		t.Fatalf("persisted status = %q, want %q (deferred, not activated)", stored.Status, domain.ConfigValidated)
	}

	// A deferred activation must be flagged (config.activation_deferred),
	// not misreported as config.activated.
	db.mu.Lock()
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	db.mu.Unlock()
	sawDeferred := false
	for _, e := range outbox {
		if e.Type == "config.activated" {
			t.Fatal("must not emit config.activated for a deferred Generation-class change")
		}
		if e.Type == "config.activation_deferred" {
			sawDeferred = true
		}
	}
	if !sawDeferred {
		t.Fatal("expected a config.activation_deferred outbox event")
	}
}

func TestActivateConfigVersionSupersedesPreviousActive(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)
	ctx := context.Background()

	first, err := svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: adminSession,
		Payload:         []byte(`{"runtime.log_level":"info"}`),
	})
	if err != nil {
		t.Fatalf("DraftConfigVersion(first) error = %v", err)
	}
	if _, err := svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{CallerSessionID: adminSession, ConfigVersionID: first.Version.ID}); err != nil {
		t.Fatalf("ValidateConfigVersion(first) error = %v", err)
	}
	if _, err := svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{CallerSessionID: adminSession, ConfigVersionID: first.Version.ID}); err != nil {
		t.Fatalf("ActivateConfigVersion(first) error = %v", err)
	}

	second, err := svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: adminSession,
		Payload:         []byte(`{"runtime.log_level":"debug"}`),
	})
	if err != nil {
		t.Fatalf("DraftConfigVersion(second) error = %v", err)
	}
	if _, err := svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{CallerSessionID: adminSession, ConfigVersionID: second.Version.ID}); err != nil {
		t.Fatalf("ValidateConfigVersion(second) error = %v", err)
	}
	secondActivated, err := svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{CallerSessionID: adminSession, ConfigVersionID: second.Version.ID})
	if err != nil {
		t.Fatalf("ActivateConfigVersion(second) error = %v", err)
	}
	if !secondActivated.Activated {
		t.Fatal("expected the second Hot-only change to activate immediately")
	}

	db.mu.Lock()
	firstStored := db.configs[first.Version.ID]
	secondStored := db.configs[second.Version.ID]
	db.mu.Unlock()
	if firstStored.Status != domain.ConfigSuperseded {
		t.Fatalf("first version status = %q, want %q", firstStored.Status, domain.ConfigSuperseded)
	}
	if secondStored.Status != domain.ConfigActive {
		t.Fatalf("second version status = %q, want %q", secondStored.Status, domain.ConfigActive)
	}
}

func TestDraftConfigVersionDeniedByPolicyDoesNotMutateState(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.DraftConfigVersion(context.Background(), app.DraftConfigVersionCommand{
		CallerSessionID: "session-nobody",
		Payload:         []byte(`{"runtime.log_level":"debug"}`),
	})
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}

	db.mu.Lock()
	configCount := len(db.configs)
	outboxLen := len(db.outbox)
	db.mu.Unlock()
	if configCount != 0 || outboxLen != 0 {
		t.Fatalf("expected zero state mutation, got configs=%d outbox=%d", configCount, outboxLen)
	}
}
