// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/test/contract"
)

// TestPostgresPassesContractSuite runs the reusable, adapter-agnostic storage
// contract suite (test/contract) against a real, migrated PostgreSQL database
// — the exit criterion "adapter passes contract tests".
func TestPostgresPassesContractSuite(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	if err := postgres.Migrate(context.Background(), pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	factory := func(t *testing.T) contract.Deps {
		truncateAll(t, pool)
		cs := mod.Bind(pool)
		return contract.Deps{
			UnitOfWork:  cs.UnitOfWork,
			Users:       cs.Users,
			Sessions:    cs.Sessions,
			Permissions: cs.Permissions,
			Config:      cs.Config,
			Outbox:      cs.Outbox,
			Credentials: cs.Credentials,

			Nodes:          cs.Nodes,
			Parts:          cs.Parts,
			Relations:      cs.Relations,
			SourceBindings: cs.SourceBindings,

			SeedRoleGrant: func(c context.Context, userID domain.UserID, roleName string, perms []domain.Permission) error {
				return seedRoleGrant(c, pool, userID, roleName, perms)
			},
		}
	}

	contract.RunAll(t, factory)
}

// TestApplicationServicesRunAgainstPostgres proves the application services
// from the earlier slices work through the real Postgres adapter with NO
// changes to the application service code — only the wired contracts differ
// from the in-memory fakes. If this required editing internal/platform/app,
// the contracts would not be adapter-agnostic.
func TestApplicationServicesRunAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	svc := app.NewService(app.Deps{
		UnitOfWork:       cs.UnitOfWork,
		Sessions:         cs.Sessions,
		Users:            cs.Users,
		Credentials:      cs.Credentials,
		Config:           cs.Config,
		Permissions:      cs.Permissions,
		Nodes:            cs.Nodes,
		Clock:            cs.Clock,
		IDs:              cs.IDs,
		ContentIDs:       cs.ContentIDs,
		Policy:           policy.NewEngine(cs.Permissions),
		Events:           noopPublisher{},
		PasswordVerifier: reversibleVerifier{},
		Capabilities:     nil, // no capabilities registered in this content integration test
		ModuleSettings:   cs.ModuleSettings,
	})

	// Bootstrap an authorized admin caller directly (admin user, an active
	// session, and a role granting the actions the flow needs). This is the
	// same fixture shape the in-memory app tests used; only the storage
	// differs.
	now := cs.Clock.Now()
	admin, err := cs.Users.Create(c, domain.User{ID: "admin", Username: "admin", Email: "admin@example.com", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("seed admin user: %v", err)
	}
	adminSession, err := cs.Sessions.Create(c, domain.Session{
		ID: "admin-session", UserID: admin.ID, DeviceID: "admin-device",
		IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), AuthStrength: domain.AuthStrengthPassword,
	})
	if err != nil {
		t.Fatalf("seed admin session: %v", err)
	}
	adminActions := []domain.Permission{
		domain.Permission(app.ActionUserCreate),
		domain.Permission(app.ActionUserRead),
		domain.Permission(app.ActionSessionCreate),
		domain.Permission(app.ActionSessionRevoke),
	}
	if err := seedRoleGrant(c, pool, admin.ID, "Administrator", adminActions); err != nil {
		t.Fatalf("seed admin role: %v", err)
	}

	// 1. Create a local user (with a password credential) via the command.
	created, err := svc.CreateLocalUser(c, app.CreateLocalUserCommand{
		CallerSessionID: adminSession.ID,
		Username:        "newuser",
		Email:           "newuser@example.com",
		DisplayName:     "New User",
		Password:        "correct horse battery staple",
	})
	if err != nil {
		t.Fatalf("CreateLocalUser: %v", err)
	}

	// The new user needs a role before it can sign itself in (policy-gated).
	if err := seedRoleGrant(c, pool, created.User.ID, "Member", []domain.Permission{domain.Permission(app.ActionSessionCreate)}); err != nil {
		t.Fatalf("seed member role: %v", err)
	}

	// 2. Authenticate → issues a real, persisted session.
	auth, err := svc.AuthenticateLocalUser(c, app.AuthenticateLocalUserCommand{
		Username: "newuser",
		Password: "correct horse battery staple",
		DeviceID: "tv-1",
	})
	if err != nil {
		t.Fatalf("AuthenticateLocalUser: %v", err)
	}
	if auth.Session.UserID != created.User.ID {
		t.Fatalf("session user = %q, want %q", auth.Session.UserID, created.User.ID)
	}

	// Grant read so the new user can query itself, then 3. validate the
	// session by using it on a query.
	if err := seedRoleGrant(c, pool, created.User.ID, "Reader", []domain.Permission{domain.Permission(app.ActionUserRead)}); err != nil {
		t.Fatalf("seed reader role: %v", err)
	}
	got, err := svc.GetUserByID(c, app.GetUserByIDQuery{CallerSessionID: auth.Session.ID, UserID: created.User.ID})
	if err != nil {
		t.Fatalf("GetUserByID with issued session: %v", err)
	}
	if got.User.Username != "newuser" {
		t.Fatalf("queried username = %q, want newuser", got.User.Username)
	}

	// 4. Revoke the session server-side, then confirm it no longer validates.
	if _, err := svc.RevokeSession(c, app.RevokeSessionCommand{CallerSessionID: adminSession.ID, TargetSessionID: auth.Session.ID}); err != nil {
		t.Fatalf("RevokeSession: %v", err)
	}
	_, err = svc.GetUserByID(c, app.GetUserByIDQuery{CallerSessionID: auth.Session.ID, UserID: created.User.ID})
	if contracts.CategoryOf(err) != contracts.Unauthenticated {
		t.Fatalf("expected Unauthenticated using revoked session, got %v", err)
	}

	// The transactional outbox should hold the events those commands emitted,
	// proving outbox rows committed with their state changes through the real
	// adapter.
	events, err := cs.Outbox.ListUnpublished(c, 100)
	if err != nil {
		t.Fatalf("ListUnpublished: %v", err)
	}
	seen := map[string]bool{}
	for _, e := range events {
		seen[e.Type] = true
	}
	for _, want := range []string{"user.created", "authentication.succeeded", "session.revoked"} {
		if !seen[want] {
			t.Fatalf("expected outbox event %q; got events %v", want, seen)
		}
	}
}

// seedRoleGrant inserts a role with the given permissions and grants it to a
// user, using raw SQL. It lives in the test package (not the reusable suite)
// because seeding is adapter-specific; the suite stays adapter-agnostic.
func seedRoleGrant(ctx context.Context, pool *pgxpool.Pool, userID domain.UserID, roleName string, perms []domain.Permission) error {
	roleID := "role-" + roleName
	permStrings := make([]string, len(perms))
	for i, p := range perms {
		permStrings[i] = string(p)
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO roles (id, name, permissions) VALUES ($1, $2, $3)
		 ON CONFLICT (id) DO UPDATE SET permissions = EXCLUDED.permissions`,
		roleID, roleName, permStrings,
	); err != nil {
		return err
	}
	if _, err := pool.Exec(ctx,
		`INSERT INTO grants (user_id, role_id) VALUES ($1, $2) ON CONFLICT DO NOTHING`,
		string(userID), roleID,
	); err != nil {
		return err
	}
	return nil
}

// noopPublisher is a contracts.EventPublisher that drops events. The real
// in-process bus is a later slice; the application service only needs a
// publisher present for its non-transactional audit path.
type noopPublisher struct{}

func (noopPublisher) Publish(context.Context, domain.Event) error { return nil }
func (noopPublisher) Subscribe(string, contracts.EventHandler) (contracts.Subscription, error) {
	return noopSubscription{}, nil
}

type noopSubscription struct{}

func (noopSubscription) Unsubscribe() {}

// reversibleVerifier is a deliberately insecure test PasswordVerifier. Real
// Argon2id hashing belongs to a future crypto adapter.
type reversibleVerifier struct{}

func (reversibleVerifier) Hash(plaintext string) (string, error) {
	return "test-hash:" + plaintext, nil
}

func (reversibleVerifier) Verify(plaintext, hash string) (bool, error) {
	return hash == "test-hash:"+plaintext, nil
}
