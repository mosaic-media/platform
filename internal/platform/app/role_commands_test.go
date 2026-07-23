// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// roleFixture seeds an admin who may create and grant roles, plus a target
// user to grant to.
func roleFixture(t *testing.T) (*app.Service, *fakeDB, *trace, domain.SessionID, domain.UserID) {
	t.Helper()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, now)

	db.seedUser(domain.User{ID: "admin", Username: "admin", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-admin", "admin", now)
	db.seedRole("admin", domain.Role{ID: "role-admin", Name: "Administrator", Permissions: []domain.Permission{
		domain.Permission(app.ActionRoleCreate),
		domain.Permission(app.ActionRoleGrant),
		domain.Permission(app.ActionPermissionRead),
		// Held because the tests below delegate them. Before ADR 0069 this
		// fixture created a role carrying content permissions its caller did
		// not have — which is precisely the escalation the delegation check
		// now refuses, so the fixture was quietly demonstrating the hole.
		domain.Permission(app.ActionContentCreate),
		domain.Permission(app.ActionContentRead),
	}})
	db.seedUser(domain.User{ID: "member", Username: "member", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})

	return svc, db, tr, "s-admin", "member"
}

func TestCreateRoleAndGrantReachesTheUser(t *testing.T) {
	svc, _, _, admin, member := roleFixture(t)
	ctx := context.Background()

	// The admin creates a role.
	created, err := svc.CreateRole(ctx, app.CreateRoleCommand{
		CallerSessionID: admin, Name: "Editor",
		Permissions: []string{string(app.ActionContentCreate), string(app.ActionContentRead)},
	})
	if err != nil {
		t.Fatalf("CreateRole: %v", err)
	}
	if created.Role.ID == "" || created.Role.Name != "Editor" || len(created.Role.Permissions) != 2 {
		t.Fatalf("role = %+v", created.Role)
	}

	// Before the grant, the member has that role nowhere.
	before, err := svc.GetRolesForUser(ctx, app.GetRolesForUserQuery{CallerSessionID: admin, TargetUserID: member})
	if err != nil {
		t.Fatalf("GetRolesForUser: %v", err)
	}
	if len(before.Roles) != 0 {
		t.Fatalf("member already has roles: %+v", before.Roles)
	}

	// The admin grants it, and now the member has it.
	if _, err := svc.GrantRole(ctx, app.GrantRoleCommand{CallerSessionID: admin, UserID: member, RoleID: created.Role.ID}); err != nil {
		t.Fatalf("GrantRole: %v", err)
	}
	after, err := svc.GetRolesForUser(ctx, app.GetRolesForUserQuery{CallerSessionID: admin, TargetUserID: member})
	if err != nil {
		t.Fatalf("GetRolesForUser: %v", err)
	}
	if len(after.Roles) != 1 || after.Roles[0].Name != "Editor" {
		t.Fatalf("member roles = %+v, want Editor", after.Roles)
	}
}

func TestRoleCommandsRequireAuthorization(t *testing.T) {
	ctx := context.Background()

	t.Run("a caller without role.create is denied and writes nothing", func(t *testing.T) {
		now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
		db := newFakeDB()
		tr := &trace{}
		svc := newTestService(db, tr, now)
		db.seedUser(domain.User{ID: "u", Username: "nobody", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
		db.seedSession("s", "u", now)

		_, err := svc.CreateRole(ctx, app.CreateRoleCommand{CallerSessionID: "s", Name: "X"})
		if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
			t.Fatalf("category = %s, want permission_denied", got)
		}
		for _, step := range tr.snapshot() {
			if step == "permissions.create_role" {
				t.Fatalf("a denied caller reached the store: %v", tr.snapshot())
			}
		}
	})

	t.Run("validation rejects an empty name and missing ids", func(t *testing.T) {
		svc, _, _, admin, member := roleFixture(t)
		if got := contracts.CategoryOf(mustErrRole(svc.CreateRole(ctx, app.CreateRoleCommand{CallerSessionID: admin}))); got != contracts.InvalidArgument {
			t.Fatalf("empty name: %s", got)
		}
		if got := contracts.CategoryOf(mustErrGrant(svc.GrantRole(ctx, app.GrantRoleCommand{CallerSessionID: admin, UserID: member}))); got != contracts.InvalidArgument {
			t.Fatalf("missing role id: %s", got)
		}
	})
}

func TestGrantMissingRoleIsConflict(t *testing.T) {
	svc, _, _, admin, member := roleFixture(t)
	_, err := svc.GrantRole(context.Background(), app.GrantRoleCommand{CallerSessionID: admin, UserID: member, RoleID: "role-nope"})
	if got := contracts.CategoryOf(err); got != contracts.Conflict {
		t.Fatalf("category = %s, want conflict", got)
	}
}

func mustErrRole(_ app.CreateRoleResult, err error) error { return err }
func mustErrGrant(_ app.GrantRoleResult, err error) error { return err }
