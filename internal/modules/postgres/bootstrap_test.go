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

// TestBootstrapAdminIsUsable pins that the seeded administrator is a real,
// working account: it signs in with its password and then exercises the
// authority it was granted, which is what makes a running binary usable by a
// human rather than only by a test that seeds directly.
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
	// The full superuser set, as main.go seeds (platform#44). Seeding what the
	// composition root seeds keeps this test about bootstrap rather than about a
	// permission set no real install has — and delegation is bounded by what the
	// grantor holds, so an account with only role.create cannot mint a role
	// carrying content.read.
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
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions,
		Tokens: cs.Tokens, Users: cs.Users, Credentials: cs.Credentials,
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
		CallerSessionID: domain.SessionID(auth.Tokens.AccessToken), Name: "Editor",
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

// TestBootstrapReconcilesTheSuperuserRole covers a failure only a pre-existing
// install shows.
//
// A preset is snapshotted into a role row when the role is created, so adding an
// action to the Platform never reaches an account that already exists. For every
// other role that is correct — an administrator should not silently widen
// because the software was upgraded — and for the superuser it is not: it is the
// root of every other grant, so an authority it does not hold can never be given
// to anyone. The symptom is a feature that quietly does not work, such as
// playback progress failing to record on an install whose admin predates
// playback.write.
func TestBootstrapReconcilesTheSuperuserRole(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	hasher := crypto.NewPasswordHasher()

	seed := bootstrap.AdminSeed{
		Username:    "owner",
		Password:    "a strong bootstrap password",
		Permissions: []domain.Permission{domain.Permission(app.ActionContentRead)},
	}
	created, err := bootstrap.EnsureAdmin(c, cs.UnitOfWork, hasher, cs.Clock, cs.IDs, seed)
	if err != nil || !created {
		t.Fatalf("first boot: created=%v err=%v", created, err)
	}

	// A later release adds an action to the preset. The account already exists,
	// so the bootstrap's create path is skipped entirely.
	seed.Permissions = append(seed.Permissions, domain.Permission(app.ActionPlaybackWrite))
	created, err = bootstrap.EnsureAdmin(c, cs.UnitOfWork, hasher, cs.Clock, cs.IDs, seed)
	if err != nil {
		t.Fatalf("second boot: %v", err)
	}
	if created {
		t.Error("the second boot re-created an existing account")
	}

	// Read it back the way the policy engine does, rather than out of the roles
	// table: the grant is what actually decides an authorisation.
	user, err := cs.Users.FindByUsername(c, seed.Username)
	if err != nil {
		t.Fatalf("FindByUsername: %v", err)
	}
	roles, err := cs.Permissions.RolesForUser(c, user.ID)
	if err != nil {
		t.Fatalf("RolesForUser: %v", err)
	}
	held := map[domain.Permission]bool{}
	for _, role := range roles {
		for _, p := range role.Permissions {
			held[p] = true
		}
	}
	if !held[domain.Permission(app.ActionPlaybackWrite)] {
		t.Error("the new action never reached the owner account; an install upgraded past it stays unable to grant it")
	}
	if !held[domain.Permission(app.ActionContentRead)] {
		t.Error("reconciling dropped an action the role already held")
	}

	// Reconciliation must not key on the role's name. Bootstrap roles carry
	// whatever name the build that created them used, so matching by name finds
	// nothing and silently does nothing on exactly the installs that need it.
	if len(roles) != 1 || roles[0].Name == "" {
		t.Fatalf("expected one named role, got %+v", roles)
	}

	// And the engine agrees, which is the only form of this that matters.
	engine := policy.NewEngine(cs.Permissions)
	decision, err := engine.Authorize(c, policy.Subject{UserID: user.ID},
		app.ActionPlaybackWrite, policy.Resource{Type: "playback"}, policy.PolicyContext{})
	if err != nil {
		t.Fatalf("Authorize: %v", err)
	}
	if !decision.Allowed {
		t.Errorf("policy still denies the reconciled action: %s", decision.Reason)
	}
}

// TestOwnerRoleIsReconciledOnEveryBoot covers the same failure on the claiming
// path.
//
// The reconciliation above lives inside EnsureAdmin, which runs only when the
// environment-variable bootstrap is configured. A server claimed through the
// setup wizard (platform#54) never calls it, so without a separate reconcile its
// owner's authority freezes at the moment of claiming — and an action added by a
// later build is then one nobody on that install can ever hold, because the root
// of every grant does not have it and cannot delegate what it lacks.
func TestOwnerRoleIsReconciledOnEveryBoot(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	var mod postgres.Module
	cs := mod.Bind(pool)

	// An unclaimed server has no owner role. That is the ordinary state of a
	// fresh install and the very first thing this runs against, so it must not
	// be an error.
	if changed, err := bootstrapReconcile(c, cs, []domain.Permission{"content.read"}); err != nil {
		t.Fatalf("reconciling an unclaimed server: %v", err)
	} else if changed {
		t.Error("an unclaimed server reported a reconciliation")
	}

	// A claim writes the owner's role directly, exactly as ClaimServer does.
	role, err := cs.Permissions.CreateRole(c, domain.Role{
		ID: "role-owner", Name: "Superuser", Permissions: []domain.Permission{"content.read"},
	})
	if err != nil {
		t.Fatalf("seed the owner role: %v", err)
	}

	current := []domain.Permission{"content.read", "session.create"}
	if changed, err := bootstrapReconcile(c, cs, current); err != nil {
		t.Fatalf("ReconcileOwnerRole: %v", err)
	} else if !changed {
		t.Fatal("a stale owner role was left as it was")
	}
	back, err := cs.Permissions.FindRole(c, role.ID)
	if err != nil {
		t.Fatalf("FindRole: %v", err)
	}
	if len(back.Permissions) != 2 {
		t.Fatalf("the owner role carries %v, want the build's current set", back.Permissions)
	}

	// Idempotent, in either order: a boot that changes nothing writes nothing.
	if changed, err := bootstrapReconcile(c, cs, []domain.Permission{"session.create", "content.read"}); err != nil {
		t.Fatalf("second ReconcileOwnerRole: %v", err)
	} else if changed {
		t.Error("a boot that changed nothing reported a write")
	}
}

func bootstrapReconcile(c context.Context, cs *postgres.ContractSet, perms []domain.Permission) (bool, error) {
	return bootstrap.ReconcileOwnerRole(c, cs.UnitOfWork, perms)
}
