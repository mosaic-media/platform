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

// What the account-creation screen may offer (ADR 0069). The rule these pin:
// a grantor never sees a permission they do not hold — absent, not disabled.

func holds(t *testing.T, actions []policy.Action, want policy.Action) bool {
	t.Helper()
	for _, a := range actions {
		if a == want {
			return true
		}
	}
	return false
}

// TestGrantorIsNeverOfferedWhatTheyLack is the whole point. A reduced
// administrator assembling a new admin must not be shown telemetry at all.
func TestGrantorIsNeverOfferedWhatTheyLack(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", roleWith("role-reduced",
		domain.Permission(app.ActionRoleCreate),
		domain.Permission(app.ActionContentRead),
		domain.Permission(app.ActionContentImport),
	))

	res, err := svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: v1.Caller{Session: string(session)}, Preset: app.PresetNameAdministrator,
	})
	if err != nil {
		t.Fatalf("GrantablePermissions: %v", err)
	}
	if holds(t, res.Available, app.ActionTelemetryRead) {
		t.Fatal("a grantor was offered a permission they do not hold")
	}
	// And the preset is narrowed to them rather than offered whole: the
	// Administrator preset contains config and module actions this caller
	// lacks, and none of them may appear pre-ticked.
	for _, a := range []policy.Action{app.ActionConfigActivate, app.ActionModuleConfigure} {
		if holds(t, res.Selected, a) {
			t.Fatalf("preset offered %q, which the grantor does not hold", a)
		}
	}
	// What they do hold, and the preset includes, is ticked.
	if !holds(t, res.Selected, app.ActionContentImport) {
		t.Fatal("expected a held preset permission to be pre-selected")
	}
}

// TestSuperuserIsOfferedEverything — the account the install was handed to can
// delegate anything, which is what makes every other account creatable.
func TestSuperuserIsOfferedEverything(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", superuserRole())

	res, err := svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: v1.Caller{Session: string(session)}, Preset: app.PresetNameAdministrator,
	})
	if err != nil {
		t.Fatalf("GrantablePermissions: %v", err)
	}
	if !holds(t, res.Available, app.ActionTelemetryRead) {
		t.Fatal("the superuser must be able to confer insight")
	}
	// Available is everything they hold; Selected is the preset, which omits
	// insight — so a superuser making an admin gets the admin set ticked and
	// telemetry present but unticked, to add deliberately.
	if holds(t, res.Selected, app.ActionTelemetryRead) {
		t.Fatal("the administrator preset should not pre-select insight")
	}
}

// TestUserPresetIsNarrowerThanWhatIsAvailable covers the second flow: creating
// an ordinary account still shows the grantor's full set, so they can add to
// the starting selection rather than being locked to it.
func TestUserPresetIsNarrowerThanWhatIsAvailable(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", superuserRole())

	res, err := svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: v1.Caller{Session: string(session)}, Preset: app.PresetNameUser,
	})
	if err != nil {
		t.Fatalf("GrantablePermissions: %v", err)
	}
	if len(res.Selected) >= len(res.Available) {
		t.Fatalf("the user preset should start narrower than what is offered: %d selected of %d",
			len(res.Selected), len(res.Available))
	}
	if !holds(t, res.Selected, app.ActionContentRead) {
		t.Fatal("an ordinary account should start able to read the library")
	}
	if holds(t, res.Selected, app.ActionRoleGrant) {
		t.Fatal("an ordinary account should not start able to grant roles")
	}
}

func TestGrantablePermissionsRejectsAnUnknownPreset(t *testing.T) {
	ctx := context.Background()
	svc, _, _, session := importFixture(t)

	_, err := svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: v1.Caller{Session: string(session)}, Preset: "Wizard",
	})
	if contracts.CategoryOf(err) != contracts.InvalidArgument {
		t.Fatalf("category = %v, want invalid_argument", contracts.CategoryOf(err))
	}
}

// TestGrantablePermissionsRequiresTheAuthorityToCreate — someone who cannot
// create an account has no business enumerating what could be granted.
func TestGrantablePermissionsRequiresTheAuthorityToCreate(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", roleWith("role-nogrant", domain.Permission(app.ActionContentRead)))

	_, err := svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: v1.Caller{Session: string(session)}, Preset: app.PresetNameUser,
	})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("category = %v, want permission_denied", contracts.CategoryOf(err))
	}
}
