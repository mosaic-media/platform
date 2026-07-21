// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql_test

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// fakeDB is a minimal in-memory backing store for a real *app.Service,
// so these GraphQL resolver tests exercise the real command/query boundary
// (authenticate, authorize, transaction, outbox) end to end through
// graphql.Do, not just a mocked Service. It mirrors
// internal/platform/app's own fakes_test.go, trimmed to what resolver
// tests need.
type fakeDB struct {
	mu        sync.Mutex
	users     map[domain.UserID]domain.User
	usernames map[string]domain.UserID
	sessions  map[domain.SessionID]domain.Session
	passwords map[domain.UserID]domain.PasswordCredential
	configs   map[domain.ConfigVersionID]domain.ConfigVersion
	roles     map[domain.UserID][]domain.Role
	rolesByID map[domain.RoleID]domain.Role
	outbox    []domain.OutboxEvent
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:     make(map[domain.UserID]domain.User),
		usernames: make(map[string]domain.UserID),
		sessions:  make(map[domain.SessionID]domain.Session),
		passwords: make(map[domain.UserID]domain.PasswordCredential),
		configs:   make(map[domain.ConfigVersionID]domain.ConfigVersion),
		roles:     make(map[domain.UserID][]domain.Role),
		rolesByID: make(map[domain.RoleID]domain.Role),
	}
}

func (db *fakeDB) seedSession(id domain.SessionID, userID domain.UserID, now time.Time) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.sessions[id] = domain.Session{
		ID: id, UserID: userID, DeviceID: "device-seed",
		IssuedAt: now.Add(-time.Hour), LastSeenAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		AuthStrength: domain.AuthStrengthPassword,
	}
}

func (db *fakeDB) seedUser(user domain.User) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.users[user.ID] = user
	db.usernames[user.Username] = user.ID
}

func (db *fakeDB) seedRole(userID domain.UserID, role domain.Role) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.roles[userID] = append(db.roles[userID], role)
}

// adminRole grants every action these resolver tests exercise.
func adminRole() domain.Role {
	return domain.Role{
		ID:   "role-admin",
		Name: "Administrator",
		Permissions: []domain.Permission{
			domain.Permission(app.ActionUserRead),
			domain.Permission(app.ActionUserList),
			domain.Permission(app.ActionUserStatusUpdate),
			domain.Permission(app.ActionSessionCreate),
			domain.Permission(app.ActionSessionRevoke),
			domain.Permission(app.ActionPermissionRead),
			domain.Permission(app.ActionConfigDraft),
			domain.Permission(app.ActionConfigValidate),
			domain.Permission(app.ActionConfigActivate),
			domain.Permission(app.ActionConfigRead),
		},
	}
}

type fakeUserStore struct{ db *fakeDB }

func (s fakeUserStore) Create(_ context.Context, user domain.User) (domain.User, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.users[user.ID] = user
	s.db.usernames[user.Username] = user.ID
	return user, nil
}

func (s fakeUserStore) FindByID(_ context.Context, id domain.UserID) (domain.User, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	user, ok := s.db.users[id]
	if !ok {
		return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
	}
	return user, nil
}

func (s fakeUserStore) FindByUsername(_ context.Context, username string) (domain.User, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	id, ok := s.db.usernames[username]
	if !ok {
		return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
	}
	return s.db.users[id], nil
}

func (s fakeUserStore) Update(_ context.Context, user domain.User) (domain.User, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.users[user.ID] = user
	return user, nil
}

func (s fakeUserStore) List(_ context.Context) ([]domain.User, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	users := make([]domain.User, 0, len(s.db.users))
	for _, u := range s.db.users {
		users = append(users, u)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.Before(users[j].CreatedAt) })
	return users, nil
}

type fakeSessionStore struct{ db *fakeDB }

func (s fakeSessionStore) Create(_ context.Context, session domain.Session) (domain.Session, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.sessions[session.ID] = session
	return session, nil
}

func (s fakeSessionStore) FindByID(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	session, ok := s.db.sessions[id]
	if !ok {
		return domain.Session{}, contracts.NewError(contracts.NotFound, "session not found")
	}
	return session, nil
}

func (s fakeSessionStore) Revoke(_ context.Context, id domain.SessionID) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	session, ok := s.db.sessions[id]
	if !ok {
		return contracts.NewError(contracts.NotFound, "session not found")
	}
	revokedAt := time.Now()
	session.RevokedAt = &revokedAt
	s.db.sessions[id] = session
	return nil
}

type fakeCredentialStore struct{ db *fakeDB }

func (s fakeCredentialStore) SavePassword(_ context.Context, credential domain.PasswordCredential) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.passwords[credential.UserID] = credential
	return nil
}

func (s fakeCredentialStore) FindPassword(_ context.Context, userID domain.UserID) (domain.PasswordCredential, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	credential, ok := s.db.passwords[userID]
	if !ok {
		return domain.PasswordCredential{}, contracts.NewError(contracts.NotFound, "password credential not found")
	}
	return credential, nil
}

func (fakeCredentialStore) SavePasskey(context.Context, domain.PasskeyCredential) error { return nil }
func (fakeCredentialStore) ListPasskeys(context.Context, domain.UserID) ([]domain.PasskeyCredential, error) {
	return nil, nil
}
func (fakeCredentialStore) SaveRecoveryFactor(context.Context, domain.RecoveryFactor) error {
	return nil
}
func (fakeCredentialStore) ConsumeRecoveryFactor(context.Context, domain.UserID, string) (domain.RecoveryFactor, error) {
	return domain.RecoveryFactor{}, contracts.NewError(contracts.NotFound, "recovery factor not found")
}

type fakePermissionStore struct{ db *fakeDB }

func (s fakePermissionStore) RolesForUser(_ context.Context, userID domain.UserID) ([]domain.Role, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	return append([]domain.Role(nil), s.db.roles[userID]...), nil
}

func (s fakePermissionStore) GrantsForUser(_ context.Context, userID domain.UserID) ([]domain.Grant, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	grants := make([]domain.Grant, 0, len(s.db.roles[userID]))
	for _, role := range s.db.roles[userID] {
		grants = append(grants, domain.Grant{UserID: userID, RoleID: role.ID})
	}
	return grants, nil
}

func (fakePermissionStore) AttributesForUser(context.Context, domain.UserID) ([]domain.Attribute, error) {
	return nil, nil
}

func (s fakePermissionStore) CreateRole(_ context.Context, role domain.Role) (domain.Role, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, exists := s.db.rolesByID[role.ID]; exists {
		return domain.Role{}, contracts.NewError(contracts.Conflict, "role already exists")
	}
	s.db.rolesByID[role.ID] = role
	return role, nil
}

func (s fakePermissionStore) GrantRole(_ context.Context, grant domain.Grant) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	role, ok := s.db.rolesByID[grant.RoleID]
	if !ok {
		return contracts.NewError(contracts.Conflict, "role does not exist")
	}
	for _, existing := range s.db.roles[grant.UserID] {
		if existing.ID == grant.RoleID {
			return contracts.NewError(contracts.Conflict, "grant already exists")
		}
	}
	s.db.roles[grant.UserID] = append(s.db.roles[grant.UserID], role)
	return nil
}

type fakeConfigStore struct{ db *fakeDB }

func (s fakeConfigStore) Save(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.configs[version.ID] = version
	return version, nil
}

func (s fakeConfigStore) Latest(_ context.Context) (domain.ConfigVersion, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var latest domain.ConfigVersion
	found := false
	for _, v := range s.db.configs {
		if !found || v.CreatedAt.After(latest.CreatedAt) {
			latest, found = v, true
		}
	}
	if !found {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
	}
	return latest, nil
}

func (s fakeConfigStore) FindByID(_ context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	version, ok := s.db.configs[id]
	if !ok {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
	}
	return version, nil
}

func (s fakeConfigStore) FindActive(_ context.Context) (domain.ConfigVersion, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	for _, v := range s.db.configs {
		if v.Status == domain.ConfigActive {
			return v, nil
		}
	}
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no active config version")
}

func (s fakeConfigStore) UpdateStatus(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.configs[version.ID]; !ok {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
	}
	if version.Status == domain.ConfigActive {
		for id, existing := range s.db.configs {
			if id != version.ID && existing.Status == domain.ConfigActive {
				return domain.ConfigVersion{}, contracts.NewError(contracts.Conflict, "another config version is already active")
			}
		}
	}
	s.db.configs[version.ID] = version
	return version, nil
}

type fakeEventOutbox struct{ db *fakeDB }

func (o fakeEventOutbox) Append(_ context.Context, event domain.OutboxEvent) error {
	o.db.mu.Lock()
	defer o.db.mu.Unlock()
	o.db.outbox = append(o.db.outbox, event)
	return nil
}
func (o fakeEventOutbox) ListUnpublished(context.Context, int) ([]domain.OutboxEvent, error) {
	return nil, nil
}
func (fakeEventOutbox) MarkPublished(context.Context, domain.EventID) error { return nil }
func (fakeEventOutbox) RecordFailure(context.Context, domain.EventID, contracts.ErrorCategory, string) error {
	return nil
}

type fakeEventPublisher struct{}

func (fakeEventPublisher) Publish(context.Context, domain.Event) error { return nil }
func (fakeEventPublisher) Subscribe(string, contracts.EventHandler) (contracts.Subscription, error) {
	return fakeSubscription{}, nil
}

type fakeSubscription struct{}

func (fakeSubscription) Unsubscribe() {}

type fakeTx struct{ db *fakeDB }

func (tx fakeTx) Users() contracts.UserStore             { return fakeUserStore{db: tx.db} }
func (tx fakeTx) Sessions() contracts.SessionStore       { return fakeSessionStore{db: tx.db} }
func (tx fakeTx) Permissions() contracts.PermissionStore { return fakePermissionStore{db: tx.db} }
func (tx fakeTx) Config() contracts.ConfigStore          { return fakeConfigStore{db: tx.db} }
func (tx fakeTx) Outbox() contracts.EventOutbox          { return fakeEventOutbox{db: tx.db} }
func (tx fakeTx) Credentials() contracts.CredentialStore { return fakeCredentialStore{db: tx.db} }

// No GraphQL resolver reaches the content model yet (ADR 0013's stores landed
// without a transport surface), so these are nil rather than fake stores
// nothing exercises. A resolver that starts using one fails loudly here.
func (fakeTx) Nodes() contracts.NodeStore                    { return nil }
func (fakeTx) Parts() contracts.PartStore                    { return nil }
func (fakeTx) Relations() contracts.RelationStore            { return nil }
func (fakeTx) SourceBindings() contracts.SourceBindingStore  { return nil }
func (fakeTx) ModuleSettings() contracts.ModuleSettingsStore { return nil }

type fakeUnitOfWork struct{ db *fakeDB }

func (u fakeUnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context, tx contracts.Tx) error) error {
	// No rollback bookkeeping: these resolver tests only assert successful
	// end-to-end routing, not the atomicity guarantees app's own test suite
	// already covers.
	return fn(ctx, fakeTx{db: u.db})
}

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

type fakeIDGenerator struct {
	mu   sync.Mutex
	next int
}

func (g *fakeIDGenerator) NewID() domain.ID {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.next++
	return domain.ID(fmt.Sprintf("id-%d", g.next))
}

type fakePasswordVerifier struct{}

func (fakePasswordVerifier) Hash(plaintext string) (string, error) {
	return "insecure-test-hash:" + plaintext, nil
}
func (fakePasswordVerifier) Verify(plaintext, hash string) (bool, error) {
	return hash == "insecure-test-hash:"+plaintext, nil
}

func newTestService(db *fakeDB, now time.Time) *app.Service {
	return app.NewService(app.Deps{
		UnitOfWork:  fakeUnitOfWork{db: db},
		Sessions:    fakeSessionStore{db: db},
		Users:       fakeUserStore{db: db},
		Credentials: fakeCredentialStore{db: db},
		Config:      fakeConfigStore{db: db},
		Permissions: fakePermissionStore{db: db},
		// No resolver reads content yet, so there is nothing for a fake node
		// store to serve. A resolver that starts using one fails loudly here.
		Nodes:            nil,
		Clock:            fakeClock{now: now},
		IDs:              &fakeIDGenerator{},
		ContentIDs:       &fakeIDGenerator{},
		Policy:           policy.NewEngine(fakePermissionStore{db: db}),
		Events:           fakeEventPublisher{},
		PasswordVerifier: fakePasswordVerifier{},
		Capabilities:     nil, // no capabilities registered in resolver tests
		ModuleSettings:   nil, // no module settings store in resolver tests
	})
}
