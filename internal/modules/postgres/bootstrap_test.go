// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/composition/bootstrap"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// TestBootstrapAdminIsUsable proves the seeded administrator is a real,
// working account: it can sign in with its password and then exercise the
// authority it was granted — the whole point of the bootstrap, which is what
// makes the running binary usable by a human rather than only by a test that
// seeds directly.
func TestBootstrapAdminIsUsable(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	hasher := crypto.NewPasswordHasher()

	const (
		username = "root"
		password = "a strong bootstrap password"
	)
	// The full superuser set, as main.go seeds (ADR 0069). It used to be two
	// permissions, which stopped working once delegation was bounded by what
	// the grantor holds: an account with only role.create could no longer mint
	// a role carrying content.read, which is the escalation that check closes.
	// Seeding what the composition root seeds keeps this test about bootstrap
	// rather than about a permission set no real install has.
	perms := make([]domain.Permission, 0)
	for _, a := range app.SuperuserActions() {
		perms = append(perms, domain.Permission(a))
	}

	created, err := bootstrap.EnsureAdmin(c, cs.UnitOfWork, hasher, cs.Clock, cs.IDs,
		bootstrap.AdminSeed{Username: username, Password: password, Permissions: perms})
	if err != nil {
		t.Fatalf("EnsureAdmin: %v", err)
	}
	if !created {
		t.Fatal("first EnsureAdmin should have created the admin")
	}

	// A second run is idempotent — the admin already exists, nothing new.
	again, err := bootstrap.EnsureAdmin(c, cs.UnitOfWork, hasher, cs.Clock, cs.IDs,
		bootstrap.AdminSeed{Username: username, Password: "different password", Permissions: perms})
	if err != nil {
		t.Fatalf("second EnsureAdmin: %v", err)
	}
	if again {
		t.Fatal("second EnsureAdmin should have been a no-op")
	}

	// The admin signs in with its real password (verified by Argon2id) and
	// then uses the authority it was granted, all through the services.
	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: hasher,
		Capabilities:   nil, // no capabilities registered in this bootstrap test
		ModuleSettings: cs.ModuleSettings,
	})

	auth, err := svc.AuthenticateLocalUser(c, app.AuthenticateLocalUserCommand{
		Username: username, Password: password, DeviceID: "cli",
	})
	if err != nil {
		t.Fatalf("bootstrapped admin could not sign in: %v", err)
	}

	// It holds role.create, so this succeeds; a caller without it would be
	// denied.
	if _, err := svc.CreateRole(c, app.CreateRoleCommand{
		CallerSessionID: auth.Session.ID, Name: "Editor",
		Permissions: []string{string(app.ActionContentRead)},
	}); err != nil {
		t.Fatalf("admin could not exercise its granted authority: %v", err)
	}

	// The wrong password is refused.
	_, err = svc.AuthenticateLocalUser(c, app.AuthenticateLocalUserCommand{
		Username: username, Password: "not the password", DeviceID: "cli",
	})
	if err == nil {
		t.Fatal("sign-in with the wrong password should fail")
	}
}
