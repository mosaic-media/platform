// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Permission presets (ADR 0069). They are starting points a grantor edits, not
// tiers — the boundary that actually holds is the delegation check, which is
// tested in delegation_test.go. What these assert is that the presets nest, so
// picking one never confers something the next one down lacks by accident.

// TestPresetsNest keeps the presets in a strict order. A user's actions must be
// within an administrator's, and an administrator's within a superuser's — so
// "pick the smaller preset" is always a reduction and never a sideways move
// that quietly adds something.
func TestPresetsNest(t *testing.T) {
	held := make(map[policy.Action]bool)
	for _, a := range app.SuperuserActions() {
		held[a] = true
	}
	for _, a := range app.AdministratorActions() {
		if !held[a] {
			t.Fatalf("administrator holds %q but the superuser does not; the superuser must be a superset", a)
		}
	}
	admin := make(map[policy.Action]bool)
	for _, a := range app.AdministratorActions() {
		admin[a] = true
	}
	for _, a := range app.UserActions() {
		if !admin[a] {
			t.Fatalf("user holds %q but an administrator does not", a)
		}
	}
	for _, a := range []policy.Action{
		app.ActionRoleCreate, app.ActionRoleGrant,
		app.ActionTelemetryRead, app.ActionTelemetryExport, app.ActionTelemetryConfigure,
	} {
		if !held[a] {
			t.Fatalf("the superuser must hold %q", a)
		}
	}
}

// TestAdministratorPresetOmitsInsight — an administrator runs the install, which
// does not require watching the people using it. It *can* be granted telemetry
// individually; this only pins what the preset starts from.
func TestAdministratorPresetOmitsInsight(t *testing.T) {
	held := make(map[policy.Action]bool)
	for _, a := range app.AdministratorActions() {
		held[a] = true
	}
	for _, a := range []policy.Action{
		app.ActionTelemetryRead, app.ActionTelemetryExport, app.ActionTelemetryConfigure,
	} {
		if held[a] {
			t.Fatalf("the administrator preset must not include %q", a)
		}
	}
	// It *does* include granting. An administrator managing accounts is the
	// normal case, and it is safe precisely because the delegation check bounds
	// what they can pass on by what they hold (ADR 0069).
	if !held[app.ActionRoleGrant] {
		t.Fatal("an administrator should be able to manage accounts")
	}
	// It must still be able to run the install, or the tier is useless.
	for _, a := range []policy.Action{
		app.ActionContentImport, app.ActionModuleConfigure, app.ActionConfigActivate,
	} {
		if !held[a] {
			t.Fatalf("an administrator must hold %q to run the install", a)
		}
	}
}

// TestSuperuserCanReadTelemetryAndAdministratorCannot exercises the difference
// through the real gate rather than asserting over a list.
func TestSuperuserCanReadTelemetryAndAdministratorCannot(t *testing.T) {
	ctx := context.Background()

	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", superuserRole())
	if _, err := svc.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{
		Caller: v1.Caller{Session: string(session)},
	}); err != nil {
		t.Fatalf("the superuser must be able to read telemetry: %v", err)
	}

	// A second install, so the administrator is not carrying the superuser's
	// grant from above.
	svc2, _, _, session2 := importFixture(t)
	_, err := svc2.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{
		Caller: v1.Caller{Session: string(session2)},
	})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("an administrator must be denied telemetry by default, got %v", contracts.CategoryOf(err))
	}
}

// TestSuperuserCanGrantInsightToAnAdministrator — a preset is a default, not
// a ceiling. Granting is deliberate and one action at a time, which is why they
// are separate actions rather than a flag.
func TestSuperuserCanGrantInsightToAnAdministrator(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)

	db.grantPermission("u-1", domain.Permission(app.ActionTelemetryRead))
	if _, err := svc.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{
		Caller: v1.Caller{Session: string(session)},
	}); err != nil {
		t.Fatalf("a granted administrator should reach telemetry: %v", err)
	}
}
