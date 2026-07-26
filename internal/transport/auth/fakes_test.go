// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth_test

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

// fakeDB is a minimal in-memory backing store for a real *app.Service, so the
// auth transport tests exercise the real command boundary — authenticate,
// authorize, transaction, outbox — rather than a mocked Service.
//
// It is the rig the GraphQL resolver tests used, carried over when ADR 0061
// retired that transport and trimmed to what auth needs: users, sessions,
// password credentials, roles and the outbox. Config and content stores are nil
// here rather than fakes nothing exercises, so a method that starts reaching
// for one fails loudly instead of passing against a stub.
type fakeDB struct {
	mu        sync.Mutex
	users     map[domain.UserID]domain.User
	usernames map[string]domain.UserID
	sessions  map[domain.SessionID]domain.Session
	// The two halves of the session credential (ADR 0102).
	accessTokens  map[string]domain.AccessToken
	refreshTokens map[string]domain.RefreshToken
	passwords     map[domain.UserID]domain.PasswordCredential
	roles         map[domain.UserID][]domain.Role
	rolesByID     map[domain.RoleID]domain.Role
	outbox        []domain.OutboxEvent
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:         make(map[domain.UserID]domain.User),
		usernames:     make(map[string]domain.UserID),
		sessions:      make(map[domain.SessionID]domain.Session),
		accessTokens:  make(map[string]domain.AccessToken),
		refreshTokens: make(map[string]domain.RefreshToken),
		passwords:     make(map[domain.UserID]domain.PasswordCredential),
		roles:         make(map[domain.UserID][]domain.Role),
		rolesByID:     make(map[domain.RoleID]domain.Role),
	}
}

func (db *fakeDB) seedUser(user domain.User) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.users[user.ID] = user
	db.usernames[user.Username] = user.ID
}

func (db *fakeDB) seedPassword(userID domain.UserID, plaintext string) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.passwords[userID] = domain.PasswordCredential{UserID: userID, Hash: "insecure-test-hash:" + plaintext}
}

func (db *fakeDB) seedRole(userID domain.UserID, role domain.Role) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.roles[userID] = append(db.roles[userID], role)
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

func (db *fakeDB) session(id domain.SessionID) (domain.Session, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()
	s, ok := db.sessions[id]
	return s, ok
}

// authRole grants exactly the two actions this transport can reach. It is
// deliberately not an all-permissions admin role: a test that passes only
// because the caller could do everything would not prove the policy gate is
// wired at all.
func authRole() domain.Role {
	return domain.Role{
		ID:   "role-auth",
		Name: "Auth",
		Permissions: []domain.Permission{
			domain.Permission(app.ActionSessionCreate),
			domain.Permission(app.ActionSessionRevoke),
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

// List answers over the seeded users, because the pre-session bootstrap reads
// it to decide whether this server has been claimed (ADR 0101). It used to be a
// stub returning nothing, which made a seeded server look unclaimed — a fake
// that lies about the store it stands in for.
func (s fakeUserStore) List(context.Context) ([]domain.User, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	out := make([]domain.User, 0, len(s.db.users))
	for _, u := range s.db.users {
		out = append(out, u)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out, nil
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

func (s fakeSessionStore) Touch(_ context.Context, id domain.SessionID, at time.Time) (domain.Session, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	session, ok := s.db.sessions[id]
	if !ok {
		return domain.Session{}, contracts.NewError(contracts.NotFound, "session not found")
	}
	session.LastSeenAt = at
	s.db.sessions[id] = session
	return session, nil
}

func (s fakeSessionStore) ListForUser(_ context.Context, userID domain.UserID, now time.Time) ([]domain.Session, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var out []domain.Session
	for _, session := range s.db.sessions {
		if session.UserID == userID && !session.Revoked() && !session.ExpiredAt(now) {
			out = append(out, session)
		}
	}
	return out, nil
}

// fakeTokenStore is the session's bearer pair (ADR 0102) in memory. Spending a
// refresh token is exactly-one-winner under the lock, like the conditional
// UPDATE it stands in for — a fake that let two callers both win would make the
// reuse-detection path pass without being exercised.
type fakeTokenStore struct{ db *fakeDB }

func (s fakeTokenStore) SaveAccess(_ context.Context, t domain.AccessToken) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.accessTokens[t.Hash] = t
	return nil
}

func (s fakeTokenStore) FindAccess(_ context.Context, hash string) (domain.AccessToken, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	t, ok := s.db.accessTokens[hash]
	if !ok {
		return domain.AccessToken{}, contracts.NewError(contracts.NotFound, "access token not found")
	}
	return t, nil
}

func (s fakeTokenStore) SaveRefresh(_ context.Context, t domain.RefreshToken) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.refreshTokens[t.Hash] = t
	return nil
}

func (s fakeTokenStore) FindRefresh(_ context.Context, hash string) (domain.RefreshToken, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	t, ok := s.db.refreshTokens[hash]
	if !ok {
		return domain.RefreshToken{}, contracts.NewError(contracts.NotFound, "refresh token not found")
	}
	return t, nil
}

func (s fakeTokenStore) MarkRefreshUsed(_ context.Context, hash string, at time.Time) (bool, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	t, ok := s.db.refreshTokens[hash]
	if !ok || t.UsedAt != nil || t.RevokedAt != nil {
		return false, nil
	}
	used := at
	t.UsedAt = &used
	s.db.refreshTokens[hash] = t
	return true, nil
}

func (s fakeTokenStore) RevokeChain(_ context.Context, chainID domain.ID, at time.Time) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	touched := map[domain.SessionID]bool{}
	for hash, t := range s.db.refreshTokens {
		if t.ChainID != chainID {
			continue
		}
		touched[t.SessionID] = true
		if t.RevokedAt == nil {
			revoked := at
			t.RevokedAt = &revoked
			s.db.refreshTokens[hash] = t
		}
	}
	for hash, t := range s.db.accessTokens {
		if touched[t.SessionID] {
			delete(s.db.accessTokens, hash)
		}
	}
	return nil
}

func (s fakeTokenStore) RevokeSession(_ context.Context, id domain.SessionID, at time.Time) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	for hash, t := range s.db.refreshTokens {
		if t.SessionID == id && t.RevokedAt == nil {
			revoked := at
			t.RevokedAt = &revoked
			s.db.refreshTokens[hash] = t
		}
	}
	for hash, t := range s.db.accessTokens {
		if t.SessionID == id {
			delete(s.db.accessTokens, hash)
		}
	}
	return nil
}

func (s fakeTokenStore) DeleteExpired(_ context.Context, before time.Time) (int, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	n := 0
	for hash, t := range s.db.accessTokens {
		if t.ExpiresAt.Before(before) {
			delete(s.db.accessTokens, hash)
			n++
		}
	}
	for hash, t := range s.db.refreshTokens {
		if t.ExpiresAt.Before(before) {
			delete(s.db.refreshTokens, hash)
			n++
		}
	}
	return n, nil
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

// CreateRole and GrantRole record, rather than accepting and forgetting. They
// used to do neither, which was fine while nothing in this package created a
// role; the claim does, and a test that could not read back what authority the
// owner ended up with would assert the one thing about claiming that matters
// least.
func (s fakePermissionStore) CreateRole(_ context.Context, role domain.Role) (domain.Role, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.rolesByID == nil {
		s.db.rolesByID = map[domain.RoleID]domain.Role{}
	}
	s.db.rolesByID[role.ID] = role
	return role, nil
}

func (s fakePermissionStore) GrantRole(_ context.Context, grant domain.Grant) error {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	role, ok := s.db.rolesByID[grant.RoleID]
	if !ok {
		return contracts.NewError(contracts.Conflict, "no such role")
	}
	s.db.roles[grant.UserID] = append(s.db.roles[grant.UserID], role)
	return nil
}
func (fakePermissionStore) SetRolePermissions(context.Context, domain.RoleID, []domain.Permission) error {
	return nil
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
func (tx fakeTx) Outbox() contracts.EventOutbox          { return fakeEventOutbox{db: tx.db} }
func (tx fakeTx) Credentials() contracts.CredentialStore { return fakeCredentialStore{db: tx.db} }
func (tx fakeTx) Tokens() contracts.TokenStore           { return fakeTokenStore{db: tx.db} }

// The auth transport reaches none of these. They are nil rather than fake
// stores nothing exercises, so a method that starts using one fails loudly.
func (fakeTx) Config() contracts.ConfigStore                 { return nil }
func (fakeTx) Nodes() contracts.NodeStore                    { return nil }
func (fakeTx) Parts() contracts.PartStore                    { return nil }
func (fakeTx) Relations() contracts.RelationStore            { return nil }
func (fakeTx) SourceBindings() contracts.SourceBindingStore  { return nil }
func (fakeTx) ModuleSettings() contracts.ModuleSettingsStore { return nil }

func (fakeTx) UserPreferences() contracts.UserPreferenceStore { return nil }
func (fakeTx) PlaybackStates() contracts.PlaybackStateStore   { return nil }

func (fakeTx) InstalledExtensions() contracts.InstalledExtensionStore { return nil }

type fakeUnitOfWork struct{ db *fakeDB }

func (u fakeUnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context, tx contracts.Tx) error) error {
	// No rollback bookkeeping: these tests assert end-to-end routing, not the
	// atomicity guarantees app's own suite already covers.
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

// fakePasswordVerifier is a reversible stand-in for Argon2id. The real hasher
// is exercised over real PostgreSQL by the end-to-end test; here the point is
// that a mismatch reaches the caller as Unauthenticated, which does not depend
// on which KDF produced the hash.
type fakePasswordVerifier struct{}

func (fakePasswordVerifier) Hash(plaintext string) (string, error) {
	return "insecure-test-hash:" + plaintext, nil
}
func (fakePasswordVerifier) Verify(plaintext, hash string) (bool, error) {
	return hash == "insecure-test-hash:"+plaintext, nil
}

func newTestService(db *fakeDB, now time.Time) *app.Service {
	return app.NewService(app.Deps{
		UnitOfWork:       fakeUnitOfWork{db: db},
		Sessions:         fakeSessionStore{db: db},
		Users:            fakeUserStore{db: db},
		Credentials:      fakeCredentialStore{db: db},
		Config:           nil,
		Permissions:      fakePermissionStore{db: db},
		Nodes:            nil,
		Clock:            fakeClock{now: now},
		IDs:              &fakeIDGenerator{},
		ContentIDs:       &fakeIDGenerator{},
		Policy:           policy.NewEngine(fakePermissionStore{db: db}),
		Events:           fakeEventPublisher{},
		PasswordVerifier: fakePasswordVerifier{},
		Capabilities:     nil,
		ModuleSettings:   nil,
		Tokens:           fakeTokenStore{db: db},
	})
}

func (fakePermissionStore) FindRole(context.Context, domain.RoleID) (domain.Role, error) {
	return domain.Role{}, nil
}
