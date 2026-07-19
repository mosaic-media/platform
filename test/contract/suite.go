// Package contract holds behavioural contract tests that any implementation
// of the Platform storage contracts must pass. The suite is deliberately
// adapter-agnostic — it imports only contracts and domain, never a concrete
// adapter — so the same tests run against the in-memory fakes and against the
// real PostgreSQL module, satisfying MEG-015 §11's rule that contract tests be
// reusable across implementations.
package contract

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// Deps is a fresh, empty set of Platform storage contracts for one subtest.
type Deps struct {
	UnitOfWork  contracts.UnitOfWork
	Users       contracts.UserStore
	Sessions    contracts.SessionStore
	Permissions contracts.PermissionStore
	Config      contracts.ConfigStore
	Outbox      contracts.EventOutbox
	Credentials contracts.CredentialStore

	// The content model (ADR 0013).
	Nodes          contracts.NodeStore
	Parts          contracts.PartStore
	Relations      contracts.RelationStore
	SourceBindings contracts.SourceBindingStore

	// SeedRoleGrant seeds a role carrying perms and grants it to userID, so
	// the read-only PermissionStore can be exercised. Optional: when nil, the
	// permission-read contract subtest is skipped. Implementations provide it
	// through their own write path (raw SQL for Postgres, map writes for a
	// fake) so the suite itself stays adapter-agnostic.
	SeedRoleGrant func(ctx context.Context, userID domain.UserID, roleName string, perms []domain.Permission) error
}

// Factory produces a fresh, empty Deps for a single subtest and is
// responsible for isolating its state from other subtests (a fresh database,
// truncated tables, or new in-memory maps).
type Factory func(t *testing.T) Deps

// RunAll runs the full storage contract suite against deps produced by the
// factory.
func RunAll(t *testing.T, newDeps Factory) {
	t.Run("UserStore", func(t *testing.T) { RunUserStoreContract(t, newDeps) })
	t.Run("SessionStore", func(t *testing.T) { RunSessionStoreContract(t, newDeps) })
	t.Run("CredentialStore", func(t *testing.T) { RunCredentialStoreContract(t, newDeps) })
	t.Run("ConfigStore", func(t *testing.T) { RunConfigStoreContract(t, newDeps) })
	t.Run("PermissionStore", func(t *testing.T) { RunPermissionStoreContract(t, newDeps) })
	t.Run("OutboxAtomicity", func(t *testing.T) { RunOutboxAtomicityContract(t, newDeps) })
	t.Run("OutboxEnvelope", func(t *testing.T) { RunOutboxEnvelopeContract(t, newDeps) })
	t.Run("OutboxFailure", func(t *testing.T) { RunOutboxFailureContract(t, newDeps) })

	t.Run("NodeStore", func(t *testing.T) { RunNodeStoreContract(t, newDeps) })
	t.Run("PartStore", func(t *testing.T) { RunPartStoreContract(t, newDeps) })
	t.Run("RelationStore", func(t *testing.T) { RunRelationStoreContract(t, newDeps) })
	t.Run("SourceBindingStore", func(t *testing.T) { RunSourceBindingStoreContract(t, newDeps) })
	t.Run("ContentNonUniformity", func(t *testing.T) { RunContentNonUniformityContract(t, newDeps) })
	t.Run("ContentAtomicity", func(t *testing.T) { RunContentAtomicityContract(t, newDeps) })
}

func ctx(t *testing.T) context.Context {
	t.Helper()
	c, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	return c
}

func requireCategory(t *testing.T, err error, want contracts.ErrorCategory) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected error with category %s, got nil", want)
	}
	if got := contracts.CategoryOf(err); got != want {
		t.Fatalf("error category = %s, want %s (err: %v)", got, want, err)
	}
}

func newUser(id domain.UserID, username string, at time.Time) domain.User {
	return domain.User{
		ID:          id,
		Username:    username,
		Email:       username + "@example.com",
		DisplayName: username,
		Status:      domain.UserActive,
		CreatedAt:   at,
		UpdatedAt:   at,
	}
}

// RunUserStoreContract verifies user persistence, lookup, uniqueness and
// update behaviour.
func RunUserStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("create and find", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		created, err := d.Users.Create(c, newUser("u-1", "alice", now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		byID, err := d.Users.FindByID(c, created.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if byID.Username != "alice" || byID.Email != "alice@example.com" {
			t.Fatalf("FindByID returned %+v", byID)
		}
		byName, err := d.Users.FindByUsername(c, "alice")
		if err != nil {
			t.Fatalf("FindByUsername: %v", err)
		}
		if byName.ID != created.ID {
			t.Fatalf("FindByUsername id = %q, want %q", byName.ID, created.ID)
		}
	})

	t.Run("find missing is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Users.FindByID(ctx(t), "nope")
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("duplicate username is conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		if _, err := d.Users.Create(c, newUser("u-1", "bob", now)); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		_, err := d.Users.Create(c, newUser("u-2", "bob", now))
		requireCategory(t, err, contracts.Conflict)
	})

	t.Run("update changes fields", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		created, err := d.Users.Create(c, newUser("u-1", "carol", now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created.DisplayName = "Carol Updated"
		created.UpdatedAt = now.Add(time.Hour)
		if _, err := d.Users.Update(c, created); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := d.Users.FindByID(c, created.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if got.DisplayName != "Carol Updated" {
			t.Fatalf("DisplayName = %q, want %q", got.DisplayName, "Carol Updated")
		}
	})

	t.Run("update missing is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Users.Update(ctx(t), newUser("ghost", "ghost", now))
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("update persists status change", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		created, err := d.Users.Create(c, newUser("u-1", "erin", now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		created.Status = domain.UserSuspended
		if _, err := d.Users.Update(c, created); err != nil {
			t.Fatalf("Update: %v", err)
		}
		got, err := d.Users.FindByID(c, created.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if got.Status != domain.UserSuspended {
			t.Fatalf("Status = %q, want %q", got.Status, domain.UserSuspended)
		}
	})

	t.Run("list returns every created user", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		if _, err := d.Users.Create(c, newUser("u-1", "frank", now)); err != nil {
			t.Fatalf("Create u-1: %v", err)
		}
		if _, err := d.Users.Create(c, newUser("u-2", "grace", now.Add(time.Minute))); err != nil {
			t.Fatalf("Create u-2: %v", err)
		}
		users, err := d.Users.List(c)
		if err != nil {
			t.Fatalf("List: %v", err)
		}
		if len(users) != 2 {
			t.Fatalf("List() returned %d users, want 2", len(users))
		}
	})
}

// RunSessionStoreContract verifies session persistence, field fidelity and
// revocation.
func RunSessionStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	seedSession := func(t *testing.T, d Deps, c context.Context) domain.Session {
		t.Helper()
		if _, err := d.Users.Create(c, newUser("u-1", "dave", now)); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		session := domain.Session{
			ID:           "s-1",
			UserID:       "u-1",
			DeviceID:     "device-1",
			IssuedAt:     now,
			LastSeenAt:   now,
			ExpiresAt:    now.Add(time.Hour),
			AuthStrength: domain.AuthStrengthPassword,
			Capabilities: []domain.Permission{"session.read", "session.write"},
		}
		created, err := d.Sessions.Create(c, session)
		if err != nil {
			t.Fatalf("Create session: %v", err)
		}
		return created
	}

	t.Run("create and find preserves fields", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		seedSession(t, d, c)
		got, err := d.Sessions.FindByID(c, "s-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if got.DeviceID != "device-1" || got.AuthStrength != domain.AuthStrengthPassword {
			t.Fatalf("session fields wrong: %+v", got)
		}
		if len(got.Capabilities) != 2 || got.Capabilities[0] != "session.read" {
			t.Fatalf("capabilities = %v", got.Capabilities)
		}
		if got.Revoked() {
			t.Fatal("fresh session must not be revoked")
		}
	})

	t.Run("find missing is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Sessions.FindByID(ctx(t), "missing")
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("revoke marks revoked", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		seedSession(t, d, c)
		if err := d.Sessions.Revoke(c, "s-1"); err != nil {
			t.Fatalf("Revoke: %v", err)
		}
		got, err := d.Sessions.FindByID(c, "s-1")
		if err != nil {
			t.Fatalf("FindByID after revoke: %v", err)
		}
		if !got.Revoked() {
			t.Fatal("session should be revoked")
		}
	})

	t.Run("revoke missing is not found", func(t *testing.T) {
		d := newDeps(t)
		err := d.Sessions.Revoke(ctx(t), "missing")
		requireCategory(t, err, contracts.NotFound)
	})
}

// RunCredentialStoreContract verifies password, passkey and recovery-factor
// persistence and the single-use recovery guarantee.
func RunCredentialStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	seedUser := func(t *testing.T, d Deps, c context.Context) domain.UserID {
		t.Helper()
		u, err := d.Users.Create(c, newUser("u-1", "erin", now))
		if err != nil {
			t.Fatalf("seed user: %v", err)
		}
		return u.ID
	}

	t.Run("password save find and replace", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		uid := seedUser(t, d, c)
		if err := d.Credentials.SavePassword(c, domain.PasswordCredential{UserID: uid, Hash: "hash-1", UpdatedAt: now}); err != nil {
			t.Fatalf("SavePassword: %v", err)
		}
		got, err := d.Credentials.FindPassword(c, uid)
		if err != nil {
			t.Fatalf("FindPassword: %v", err)
		}
		if got.Hash != "hash-1" {
			t.Fatalf("hash = %q, want hash-1", got.Hash)
		}
		// Re-save replaces (password change).
		if err := d.Credentials.SavePassword(c, domain.PasswordCredential{UserID: uid, Hash: "hash-2", UpdatedAt: now.Add(time.Hour)}); err != nil {
			t.Fatalf("SavePassword replace: %v", err)
		}
		got, err = d.Credentials.FindPassword(c, uid)
		if err != nil {
			t.Fatalf("FindPassword after replace: %v", err)
		}
		if got.Hash != "hash-2" {
			t.Fatalf("hash after replace = %q, want hash-2", got.Hash)
		}
	})

	t.Run("find missing password is not found", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		uid := seedUser(t, d, c)
		_, err := d.Credentials.FindPassword(c, uid)
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("passkey save and list", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		uid := seedUser(t, d, c)
		pk := domain.PasskeyCredential{UserID: uid, CredentialID: "cred-1", PublicKey: []byte{1, 2, 3}, CreatedAt: now}
		if err := d.Credentials.SavePasskey(c, pk); err != nil {
			t.Fatalf("SavePasskey: %v", err)
		}
		list, err := d.Credentials.ListPasskeys(c, uid)
		if err != nil {
			t.Fatalf("ListPasskeys: %v", err)
		}
		if len(list) != 1 || list[0].CredentialID != "cred-1" || len(list[0].PublicKey) != 3 {
			t.Fatalf("passkeys = %+v", list)
		}
	})

	t.Run("recovery factor is single use", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		uid := seedUser(t, d, c)
		if err := d.Credentials.SaveRecoveryFactor(c, domain.RecoveryFactor{UserID: uid, CodeHash: "code-1", CreatedAt: now}); err != nil {
			t.Fatalf("SaveRecoveryFactor: %v", err)
		}
		consumed, err := d.Credentials.ConsumeRecoveryFactor(c, uid, "code-1")
		if err != nil {
			t.Fatalf("ConsumeRecoveryFactor: %v", err)
		}
		if !consumed.Consumed() {
			t.Fatal("consumed factor should report Consumed()")
		}
		// Second consume must fail: a recovery code is spent at most once.
		_, err = d.Credentials.ConsumeRecoveryFactor(c, uid, "code-1")
		requireCategory(t, err, contracts.NotFound)
	})
}

// RunConfigStoreContract verifies config version persistence, latest
// selection and the MEG-015 §08 activation status bookkeeping.
func RunConfigStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("latest on empty is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Config.Latest(ctx(t))
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("save find and latest", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		v1 := domain.ConfigVersion{ID: "cv-1", Payload: []byte(`{"v":1}`), Status: domain.ConfigDraft, CreatedAt: now}
		v2 := domain.ConfigVersion{ID: "cv-2", Payload: []byte(`{"v":2}`), Status: domain.ConfigDraft, CreatedAt: now.Add(time.Minute)}
		if _, err := d.Config.Save(c, v1); err != nil {
			t.Fatalf("Save v1: %v", err)
		}
		if _, err := d.Config.Save(c, v2); err != nil {
			t.Fatalf("Save v2: %v", err)
		}
		byID, err := d.Config.FindByID(c, "cv-1")
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if string(byID.Payload) != `{"v":1}` {
			t.Fatalf("payload = %s", byID.Payload)
		}
		if byID.Status != domain.ConfigDraft {
			t.Fatalf("status = %q, want %q", byID.Status, domain.ConfigDraft)
		}
		latest, err := d.Config.Latest(c)
		if err != nil {
			t.Fatalf("Latest: %v", err)
		}
		if latest.ID != "cv-2" {
			t.Fatalf("latest id = %q, want cv-2", latest.ID)
		}
	})

	t.Run("find active on empty is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Config.FindActive(ctx(t))
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("update status transitions to active and back out on supersede", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		v := domain.ConfigVersion{ID: "cv-active", Payload: []byte(`{"v":1}`), Status: domain.ConfigDraft, CreatedAt: now}
		if _, err := d.Config.Save(c, v); err != nil {
			t.Fatalf("Save: %v", err)
		}

		validated := v.MarkValidated(now, "ok")
		if _, err := d.Config.UpdateStatus(c, validated); err != nil {
			t.Fatalf("UpdateStatus(validated): %v", err)
		}

		active := validated.MarkActive(now)
		if _, err := d.Config.UpdateStatus(c, active); err != nil {
			t.Fatalf("UpdateStatus(active): %v", err)
		}

		found, err := d.Config.FindActive(c)
		if err != nil {
			t.Fatalf("FindActive: %v", err)
		}
		if found.ID != "cv-active" || found.Status != domain.ConfigActive {
			t.Fatalf("FindActive = %+v, want cv-active/active", found)
		}

		superseded := active.MarkSuperseded(now.Add(time.Minute))
		if _, err := d.Config.UpdateStatus(c, superseded); err != nil {
			t.Fatalf("UpdateStatus(superseded): %v", err)
		}
		if _, err := d.Config.FindActive(c); contracts.CategoryOf(err) != contracts.NotFound {
			t.Fatalf("FindActive after supersede: err = %v, want NotFound", err)
		}
	})

	t.Run("update status on unknown id is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Config.UpdateStatus(ctx(t), domain.ConfigVersion{ID: "does-not-exist", Status: domain.ConfigActive})
		requireCategory(t, err, contracts.NotFound)
	})

	t.Run("only one version may be active at a time", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		v1 := domain.ConfigVersion{ID: "cv-first", Payload: []byte(`{"v":1}`), Status: domain.ConfigDraft, CreatedAt: now}
		v2 := domain.ConfigVersion{ID: "cv-second", Payload: []byte(`{"v":2}`), Status: domain.ConfigDraft, CreatedAt: now.Add(time.Minute)}
		if _, err := d.Config.Save(c, v1); err != nil {
			t.Fatalf("Save v1: %v", err)
		}
		if _, err := d.Config.Save(c, v2); err != nil {
			t.Fatalf("Save v2: %v", err)
		}
		if _, err := d.Config.UpdateStatus(c, v1.MarkActive(now)); err != nil {
			t.Fatalf("activate v1: %v", err)
		}
		// Activating v2 without first superseding v1 must be rejected by the
		// single-active-version invariant rather than silently leaving two
		// versions Active.
		_, err := d.Config.UpdateStatus(c, v2.MarkActive(now.Add(time.Minute)))
		requireCategory(t, err, contracts.Conflict)
	})
}

// RunPermissionStoreContract verifies role/grant lookup. It is skipped when
// the factory does not provide a SeedRoleGrant hook, because PermissionStore
// is read-only and the suite has no adapter-agnostic way to seed roles.
func RunPermissionStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("roles for user", func(t *testing.T) {
		d := newDeps(t)
		if d.SeedRoleGrant == nil {
			t.Skip("factory does not provide SeedRoleGrant")
		}
		c := ctx(t)
		if _, err := d.Users.Create(c, newUser("u-1", "frank", now)); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if err := d.SeedRoleGrant(c, "u-1", "Administrator", []domain.Permission{"user.create", "user.read"}); err != nil {
			t.Fatalf("SeedRoleGrant: %v", err)
		}
		roles, err := d.Permissions.RolesForUser(c, "u-1")
		if err != nil {
			t.Fatalf("RolesForUser: %v", err)
		}
		if len(roles) != 1 || roles[0].Name != "Administrator" {
			t.Fatalf("roles = %+v", roles)
		}
		if len(roles[0].Permissions) != 2 {
			t.Fatalf("permissions = %v", roles[0].Permissions)
		}
		grants, err := d.Permissions.GrantsForUser(c, "u-1")
		if err != nil {
			t.Fatalf("GrantsForUser: %v", err)
		}
		if len(grants) != 1 {
			t.Fatalf("grants = %+v", grants)
		}
	})

	t.Run("no roles for unknown user", func(t *testing.T) {
		d := newDeps(t)
		roles, err := d.Permissions.RolesForUser(ctx(t), "nobody")
		if err != nil {
			t.Fatalf("RolesForUser: %v", err)
		}
		if len(roles) != 0 {
			t.Fatalf("expected no roles, got %+v", roles)
		}
	})

	// CreateRole and GrantRole are the write path — the first way authority is
	// assigned through the Platform rather than seeded out of band.
	t.Run("create a role, grant it, and read it back", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		if _, err := d.Users.Create(c, newUser("u-1", "grace", now)); err != nil {
			t.Fatalf("seed user: %v", err)
		}

		role, err := d.Permissions.CreateRole(c, domain.Role{
			ID: "role-editor", Name: "Editor",
			Permissions: []domain.Permission{"content.create", "content.read"},
		})
		if err != nil {
			t.Fatalf("CreateRole: %v", err)
		}
		if err := d.Permissions.GrantRole(c, domain.Grant{UserID: "u-1", RoleID: role.ID}); err != nil {
			t.Fatalf("GrantRole: %v", err)
		}

		roles, err := d.Permissions.RolesForUser(c, "u-1")
		if err != nil {
			t.Fatalf("RolesForUser: %v", err)
		}
		if len(roles) != 1 || roles[0].Name != "Editor" || len(roles[0].Permissions) != 2 {
			t.Fatalf("roles = %+v", roles)
		}
	})

	t.Run("a duplicate role is a conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		role := domain.Role{ID: "role-dup", Name: "Dup", Permissions: []domain.Permission{"content.read"}}
		if _, err := d.Permissions.CreateRole(c, role); err != nil {
			t.Fatalf("first CreateRole: %v", err)
		}
		_, err := d.Permissions.CreateRole(c, role)
		requireCategory(t, err, contracts.Conflict)
	})

	t.Run("granting a role that does not exist is a conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		if _, err := d.Users.Create(c, newUser("u-1", "heidi", now)); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		err := d.Permissions.GrantRole(c, domain.Grant{UserID: "u-1", RoleID: "role-missing"})
		requireCategory(t, err, contracts.Conflict)
	})

	t.Run("a duplicate grant is a conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		if _, err := d.Users.Create(c, newUser("u-1", "ivan", now)); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		if _, err := d.Permissions.CreateRole(c, domain.Role{ID: "role-x", Name: "X"}); err != nil {
			t.Fatalf("CreateRole: %v", err)
		}
		if err := d.Permissions.GrantRole(c, domain.Grant{UserID: "u-1", RoleID: "role-x"}); err != nil {
			t.Fatalf("first GrantRole: %v", err)
		}
		err := d.Permissions.GrantRole(c, domain.Grant{UserID: "u-1", RoleID: "role-x"})
		requireCategory(t, err, contracts.Conflict)
	})
}

// RunOutboxAtomicityContract is the core storage guarantee: a state write and
// its outbox event commit together or not at all (MEG-015 §05), and a
// concurrent uniqueness conflict is reported as Conflict rather than a raw
// driver error.
func RunOutboxAtomicityContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("commit persists state and event together", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		err := d.UnitOfWork.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
			if _, err := tx.Users().Create(c, newUser("u-1", "grace", now)); err != nil {
				return err
			}
			return tx.Outbox().Append(c, domain.OutboxEvent{Event: domain.Event{
				ID: "e-1", Type: "user.created", Payload: []byte("grace"), OccurredAt: now,
			}})
		})
		if err != nil {
			t.Fatalf("WithinTx: %v", err)
		}
		if _, err := d.Users.FindByID(c, "u-1"); err != nil {
			t.Fatalf("user should be committed: %v", err)
		}
		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 1 || events[0].ID != "e-1" {
			t.Fatalf("expected one committed event, got %+v", events)
		}
	})

	t.Run("rollback persists neither state nor event", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		sentinel := errors.New("forced rollback")
		err := d.UnitOfWork.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
			if _, err := tx.Users().Create(c, newUser("u-2", "heidi", now)); err != nil {
				return err
			}
			if err := tx.Outbox().Append(c, domain.OutboxEvent{Event: domain.Event{
				ID: "e-2", Type: "user.created", Payload: []byte("heidi"), OccurredAt: now,
			}}); err != nil {
				return err
			}
			return sentinel // force rollback after both writes
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("WithinTx error = %v, want sentinel", err)
		}
		// Neither write may survive the rollback.
		if _, err := d.Users.FindByID(c, "u-2"); contracts.CategoryOf(err) != contracts.NotFound {
			t.Fatalf("user must not be committed after rollback, got err=%v", err)
		}
		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("event must not be committed after rollback, got %+v", events)
		}
	})

	t.Run("concurrent unique inserts yield exactly one conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		const workers = 8
		var wg sync.WaitGroup
		results := make([]error, workers)
		start := make(chan struct{})
		for i := 0; i < workers; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				<-start // release all goroutines together to maximise contention
				results[i] = d.UnitOfWork.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
					// All workers race to insert the same username.
					_, err := tx.Users().Create(c, newUser(domain.UserID("u-race-"+string(rune('a'+i))), "shared", now))
					return err
				})
			}(i)
		}
		close(start)
		wg.Wait()

		success, conflicts, other := 0, 0, 0
		for _, err := range results {
			switch {
			case err == nil:
				success++
			case contracts.CategoryOf(err) == contracts.Conflict:
				conflicts++
			default:
				other++
			}
		}
		if success != 1 {
			t.Fatalf("expected exactly 1 success, got %d (conflicts=%d other=%d)", success, conflicts, other)
		}
		if other != 0 {
			t.Fatalf("expected all failures to be Conflict, got %d non-conflict failures", other)
		}
		if conflicts != workers-1 {
			t.Fatalf("expected %d conflicts, got %d", workers-1, conflicts)
		}
	})
}

// RunOutboxEnvelopeContract verifies the full event envelope (MEG-015 §06)
// round-trips through storage.
func RunOutboxEnvelopeContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("envelope round-trips", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		want := domain.OutboxEvent{Event: domain.Event{
			ID:             "evt-1",
			Type:           "user.created.v1",
			OccurredAt:     now,
			RecordedAt:     now.Add(time.Millisecond),
			Actor:          "user-admin",
			TenantScope:    "local",
			CorrelationID:  "corr-1",
			CausationID:    "cause-1",
			Payload:        []byte(`{"username":"alice"}`),
			RedactionClass: domain.RedactionSensitive,
		}}
		if err := d.Outbox.Append(c, want); err != nil {
			t.Fatalf("Append: %v", err)
		}

		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 1 {
			t.Fatalf("expected 1 event, got %d", len(events))
		}
		got := events[0]
		if got.ID != want.ID || got.Type != want.Type || got.Actor != want.Actor ||
			got.TenantScope != want.TenantScope || got.CorrelationID != want.CorrelationID ||
			got.CausationID != want.CausationID || got.RedactionClass != want.RedactionClass ||
			string(got.Payload) != string(want.Payload) {
			t.Fatalf("envelope did not round-trip: got %+v", got.Event)
		}
		if !got.OccurredAt.Equal(want.OccurredAt) || !got.RecordedAt.Equal(want.RecordedAt) {
			t.Fatalf("timestamps did not round-trip: occurred=%v recorded=%v", got.OccurredAt, got.RecordedAt)
		}
		if got.Published() || got.DeadLettered || got.Attempts != 0 {
			t.Fatalf("a fresh event should be unpublished, not dead-lettered, zero attempts: %+v", got)
		}
	})

	t.Run("unclassified payload defaults to redacted", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		// No RedactionClass set: the store must default to a redact-safe class
		// rather than leaving the payload unclassified.
		if err := d.Outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
			ID: "evt-2", Type: "t", OccurredAt: now, RecordedAt: now, Payload: []byte("x"),
		}}); err != nil {
			t.Fatalf("Append: %v", err)
		}
		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 1 || events[0].RedactionClass != domain.RedactionSensitive {
			t.Fatalf("expected default RedactionSensitive, got %+v", events)
		}
	})
}

// RunOutboxFailureContract verifies delivery failure bookkeeping (MEG-015
// §06 — Failure Behaviour): attempts accumulate, a retry is scheduled, and an
// event is dead-lettered (and dropped from the deliverable set) once retries
// are exhausted.
func RunOutboxFailureContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	appendEvent := func(t *testing.T, d Deps, c context.Context, id domain.EventID) {
		t.Helper()
		if err := d.Outbox.Append(c, domain.OutboxEvent{Event: domain.Event{
			ID: id, Type: "t", OccurredAt: now, RecordedAt: now, Payload: []byte("p"),
		}}); err != nil {
			t.Fatalf("Append: %v", err)
		}
	}

	t.Run("first failure removes event from immediate deliverability", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		appendEvent(t, d, c, "evt-1")

		// Before any failure, a freshly appended event is immediately
		// deliverable.
		before, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished (before failure): %v", err)
		}
		if len(before) != 1 {
			t.Fatalf("expected 1 deliverable event before any failure, got %d", len(before))
		}

		if err := d.Outbox.RecordFailure(c, "evt-1", contracts.Unavailable, "outbox-worker"); err != nil {
			t.Fatalf("RecordFailure: %v", err)
		}

		// Immediately after a failure the event is waiting out its retry
		// backoff (MEG-015 §06 — Failure Behaviour) and must not be
		// immediately redelivered — this is what stops the worker from
		// hot-looping a retry before the backoff elapses. Exact bookkeeping
		// field values (attempts, category, owning component, next retry
		// time) are adapter-specific storage detail, not something this
		// contract exposes a read path for; they are verified against the
		// PostgreSQL implementation directly.
		after, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished (after failure): %v", err)
		}
		if len(after) != 0 {
			t.Fatalf("event must not be immediately deliverable after a failure, got %d", len(after))
		}
	})

	t.Run("exhausting retries dead-letters and removes from deliverable set", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		appendEvent(t, d, c, "evt-2")
		policy := domain.DefaultDeliveryPolicy()
		for i := 0; i < policy.MaxAttempts; i++ {
			if err := d.Outbox.RecordFailure(c, "evt-2", contracts.Internal, "outbox-worker"); err != nil {
				t.Fatalf("RecordFailure #%d: %v", i+1, err)
			}
		}
		// After MaxAttempts failures the event is dead-lettered, so it drops
		// out of the deliverable (unpublished, non-dead-lettered) set.
		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("dead-lettered event must not be deliverable, got %d", len(events))
		}
	})

	t.Run("failure on missing event is not found", func(t *testing.T) {
		d := newDeps(t)
		err := d.Outbox.RecordFailure(ctx(t), "nope", contracts.Internal, "outbox-worker")
		requireCategory(t, err, contracts.NotFound)
	})
}
