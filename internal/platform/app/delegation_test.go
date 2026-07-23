// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// Privilege cannot escalate through delegation (ADR 0069). Without this rule
// role.create is equivalent to every permission — mint a role holding anything,
// grant it to yourself — so these are the tests that make granular permissions
// safe to hand out at all.

// roleWith builds a role carrying exactly perms.
func roleWith(id domain.RoleID, perms ...domain.Permission) domain.Role {
	return domain.Role{ID: id, Name: string(id), Permissions: perms}
}

// TestCannotCreateARoleCarryingAuthorityYouLack is the escalation this closes.
// The caller may create roles and may import content — and must not be able to
// mint a role that reads telemetry.
func TestCannotCreateARoleCarryingAuthorityYouLack(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", roleWith("role-limited",
		domain.Permission(app.ActionRoleCreate),
		domain.Permission(app.ActionContentImport),
	))

	_, err := svc.CreateRole(ctx, app.CreateRoleCommand{
		CallerSessionID: session,
		Name:            "sneaky",
		Permissions:     []string{string(app.ActionContentImport), string(app.ActionTelemetryRead)},
	})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("category = %v, want permission_denied", contracts.CategoryOf(err))
	}
	// The refusal names what was missing: someone assembling a role from
	// twenty checkboxes cannot act on "denied", and the information is not
	// sensitive — they know their own permissions and chose the set.
	if !strings.Contains(err.Error(), string(app.ActionTelemetryRead)) {
		t.Fatalf("the refusal should name the missing permission, got %q", err)
	}
	if strings.Contains(err.Error(), string(app.ActionContentImport)) {
		t.Fatalf("the refusal should not name permissions the caller does hold, got %q", err)
	}
}

// TestCanCreateARoleWithinYourOwnAuthority — the rule bounds delegation, it does
// not forbid it. An administrator with a reduced set can still make users, just
// not ones exceeding itself.
func TestCanCreateARoleWithinYourOwnAuthority(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", roleWith("role-limited",
		domain.Permission(app.ActionRoleCreate),
		domain.Permission(app.ActionContentImport),
		domain.Permission(app.ActionContentRead),
	))

	res, err := svc.CreateRole(ctx, app.CreateRoleCommand{
		CallerSessionID: session,
		Name:            "viewer",
		Permissions:     []string{string(app.ActionContentRead)},
	})
	if err != nil {
		t.Fatalf("CreateRole within own authority: %v", err)
	}
	if len(res.Role.Permissions) != 1 {
		t.Fatalf("role = %+v", res.Role)
	}
}

// TestSuperuserCanCreateAnyRole — holding everything means delegating anything,
// which is what makes the first account able to bootstrap the others.
func TestSuperuserCanCreateAnyRole(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", superuserRole())

	if _, err := svc.CreateRole(ctx, app.CreateRoleCommand{
		CallerSessionID: session,
		Name:            "insight",
		Permissions:     []string{string(app.ActionTelemetryRead), string(app.ActionRoleGrant)},
	}); err != nil {
		t.Fatalf("the superuser must be able to delegate anything: %v", err)
	}
}

// TestCannotGrantAnExistingRoleBeyondYourAuthority closes the other half. A role
// the superuser created must not become an escalation path for someone who can
// grant but does not hold what it carries.
func TestCannotGrantAnExistingRoleBeyondYourAuthority(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)

	// A powerful role exists, created by someone else.
	db.seedRole("u-super", roleWith("role-powerful",
		domain.Permission(app.ActionTelemetryRead),
	))
	// The caller may grant, but holds no telemetry.
	db.replaceRoles("u-1", roleWith("role-granter",
		domain.Permission(app.ActionRoleGrant),
	))

	_, err := svc.GrantRole(ctx, app.GrantRoleCommand{
		CallerSessionID: session,
		UserID:          "u-1",
		RoleID:          "role-powerful",
	})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("category = %v, want permission_denied — granting an existing role must be bounded too",
			contracts.CategoryOf(err))
	}
}

// TestCanGrantARoleWithinYourAuthority
func TestCanGrantARoleWithinYourAuthority(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	// Seeded against a third user, so "role-reader" exists without u-2 already
	// holding it — granting someone a role they have is a Conflict and would
	// mask what this test is about.
	db.seedRole("u-3", roleWith("role-reader", domain.Permission(app.ActionContentRead)))
	db.replaceRoles("u-1", roleWith("role-granter",
		domain.Permission(app.ActionRoleGrant),
		domain.Permission(app.ActionContentRead),
	))

	if _, err := svc.GrantRole(ctx, app.GrantRoleCommand{
		CallerSessionID: session,
		UserID:          "u-2",
		RoleID:          "role-reader",
	}); err != nil {
		t.Fatalf("granting within own authority: %v", err)
	}
}

// TestEmptyRoleIsAlwaysDelegable — a role carrying nothing escalates nothing,
// and refusing it would make "create a role then add to it" impossible.
func TestEmptyRoleIsAlwaysDelegable(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.replaceRoles("u-1", roleWith("role-granter", domain.Permission(app.ActionRoleCreate)))

	if _, err := svc.CreateRole(ctx, app.CreateRoleCommand{
		CallerSessionID: session, Name: "empty", Permissions: nil,
	}); err != nil {
		t.Fatalf("an empty role escalates nothing and must be allowed: %v", err)
	}
}
