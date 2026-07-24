// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// trace records the order in which contract and policy boundary calls
// occur, so tests can assert the command/query order actually
// happened rather than merely that the final result looks right.
type trace struct {
	mu    sync.Mutex
	steps []string
}

func (t *trace) record(step string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.steps = append(t.steps, step)
}

func (t *trace) snapshot() []string {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]string(nil), t.steps...)
}

// fakeDBSnapshot is a point-in-time copy of every mutable fakeDB field, so
// fakeUnitOfWork can restore it on rollback.
type fakeDBSnapshot struct {
	users           map[domain.UserID]domain.User
	usernames       map[string]domain.UserID
	sessions        map[domain.SessionID]domain.Session
	passwords       map[domain.UserID]domain.PasswordCredential
	configs         map[domain.ConfigVersionID]domain.ConfigVersion
	outbox          []domain.OutboxEvent
	nodes           map[v1.NodeID]v1.Node
	parts           map[v1.PartID]v1.Part
	relations       map[v1.RelationID]v1.Relation
	bindings        map[v1.SourceBindingID]v1.SourceBinding
	moduleSettings  map[string]domain.ModuleSettings
	userPreferences map[string]domain.UserPreference
	playbackStates  map[playbackKey]v1.PlaybackState
}

// fakeDB is the shared backing store behind every fake contract in this
// package. The same data is reachable directly (for authentication and
// query reads) and through a fakeTx (for the command write path), mirroring
// how a real adapter would expose one database through several contracts.
type fakeDB struct {
	mu        sync.Mutex
	users     map[domain.UserID]domain.User
	usernames map[string]domain.UserID
	sessions  map[domain.SessionID]domain.Session
	passwords map[domain.UserID]domain.PasswordCredential
	configs   map[domain.ConfigVersionID]domain.ConfigVersion
	outbox    []domain.OutboxEvent
	// roles is never written by any Service command in this slice — it is
	// a fixture the tests seed directly, standing in for the admin-
	// controlled permission assignment this slice does not build a command
	// for.
	roles map[domain.UserID][]domain.Role
	// rolesByID is the role catalogue CreateRole writes and GrantRole reads.
	rolesByID map[domain.RoleID]domain.Role

	// nodes and parts back the content commands and queries; relations and
	// bindings back the graph and identity commands.
	nodes           map[v1.NodeID]v1.Node
	parts           map[v1.PartID]v1.Part
	relations       map[v1.RelationID]v1.Relation
	bindings        map[v1.SourceBindingID]v1.SourceBinding
	moduleSettings  map[string]domain.ModuleSettings
	userPreferences map[string]domain.UserPreference
	// playbackStates is the first per-user content state (ADR 0046), so it is
	// the first fake here keyed by a pair rather than by an id.
	playbackStates map[playbackKey]v1.PlaybackState
}

// playbackKey is the (user, node) pair playback state is stored under.
type playbackKey struct {
	user domain.UserID
	node v1.NodeID
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:           make(map[domain.UserID]domain.User),
		usernames:       make(map[string]domain.UserID),
		sessions:        make(map[domain.SessionID]domain.Session),
		passwords:       make(map[domain.UserID]domain.PasswordCredential),
		configs:         make(map[domain.ConfigVersionID]domain.ConfigVersion),
		roles:           make(map[domain.UserID][]domain.Role),
		rolesByID:       make(map[domain.RoleID]domain.Role),
		nodes:           make(map[v1.NodeID]v1.Node),
		parts:           make(map[v1.PartID]v1.Part),
		relations:       make(map[v1.RelationID]v1.Relation),
		bindings:        make(map[v1.SourceBindingID]v1.SourceBinding),
		moduleSettings:  make(map[string]domain.ModuleSettings),
		userPreferences: make(map[string]domain.UserPreference),
		playbackStates:  make(map[playbackKey]v1.PlaybackState),
	}
}

func (db *fakeDB) seedSession(id domain.SessionID, userID domain.UserID, now time.Time) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.sessions[id] = domain.Session{
		ID:           id,
		UserID:       userID,
		DeviceID:     "device-seed",
		IssuedAt:     now.Add(-time.Hour),
		LastSeenAt:   now.Add(-time.Hour),
		ExpiresAt:    now.Add(time.Hour),
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
	// Registered by id as well. In the real schema a role is one row that
	// grants reference; seeding only the per-user view made a seeded role
	// invisible to FindRole and to GrantRole, which is a property of the fake
	// rather than of the Platform.
	db.rolesByID[role.ID] = role
}

// seedActiveConfig stores an Active configuration version carrying fields, so
// a test can exercise a value read from configuration rather than a default.
func (db *fakeDB) seedActiveConfig(t *testing.T, fields map[string]any) {
	t.Helper()
	payload, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("marshal config payload: %v", err)
	}
	db.mu.Lock()
	defer db.mu.Unlock()
	db.configs["cfg-active"] = domain.ConfigVersion{
		ID:      "cfg-active",
		Payload: payload,
		Status:  domain.ConfigActive,
	}
}

// replaceRoles makes role the user's *only* role.
//
// seedRole appends, and importFixture already seeds an administrator — so
// seeding a deliberately narrow role on top of that leaves the caller holding
// both, and a test meaning to prove "this grantor is limited" proves nothing.
// This is for exactly that case.
func (db *fakeDB) replaceRoles(userID domain.UserID, role domain.Role) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.roles[userID] = []domain.Role{role}
	db.rolesByID[role.ID] = role
}

// grantPermission adds one permission to a user, as its own role.
//
// Separate from adminRole on purpose: the actions that are deliberately *not*
// part of an administrator's default grants — telemetry.read, and audit.read
// when it exists — must be grantable in a test without widening the role that
// stands for "an ordinary admin", or the tests proving they are withheld would
// quietly stop proving it.
func (db *fakeDB) grantPermission(userID domain.UserID, perm domain.Permission) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.roles[userID] = append(db.roles[userID], domain.Role{
		ID:          domain.RoleID("role-" + string(perm)),
		Name:        string(perm),
		Permissions: []domain.Permission{perm},
	})
}

// fakeTelemetryQueryStore implements contracts.TelemetryQueryStore with one
// canned record, enough to prove a filter reaches the store and a gate opens.
type fakeTelemetryQueryStore struct{}

func (fakeTelemetryQueryStore) QueryLogs(_ context.Context, f domain.TelemetryLogFilter) ([]domain.TelemetryLogRecord, error) {
	return []domain.TelemetryLogRecord{{
		Level:     "warn",
		Component: f.Component,
		Message:   "canned",
		Fields:    []byte("{}"),
	}}, nil
}

func (fakeTelemetryQueryStore) Trace(context.Context, string) ([]domain.TelemetrySpanRecord, error) {
	return nil, nil
}

func (fakeTelemetryQueryStore) TraceLogs(context.Context, string) ([]domain.TelemetryLogRecord, error) {
	return nil, nil
}

func (fakeTelemetryQueryStore) RecentTraces(context.Context, domain.TelemetryTraceFilter) ([]domain.TelemetryTraceSummary, error) {
	return nil, nil
}

// adminRole is the Administrator preset (ADR 0069): everything operational,
// and no insight.
//
// Derived from app.AdministratorActions rather than listed by hand, so the
// fixture cannot drift from the real tier. That matters most for the actions it
// deliberately lacks: a hand-written list that quietly gained telemetry.read
// would turn every test asserting the tier boundary into one that asserts
// nothing.
func adminRole() domain.Role {
	return roleFrom("role-admin", app.PresetNameAdministrator, app.AdministratorActions())
}

// superuserRole is the first user's tier: every action, including the ones an
// administrator has to be granted individually.
func superuserRole() domain.Role {
	return roleFrom("role-superuser", app.PresetNameSuperuser, app.SuperuserActions())
}

func roleFrom(id domain.RoleID, name string, actions []policy.Action) domain.Role {
	perms := make([]domain.Permission, len(actions))
	for i, a := range actions {
		perms[i] = domain.Permission(a)
	}
	return domain.Role{ID: id, Name: name, Permissions: perms}
}

func (db *fakeDB) snapshot() fakeDBSnapshot {
	db.mu.Lock()
	defer db.mu.Unlock()

	users := make(map[domain.UserID]domain.User, len(db.users))
	for k, v := range db.users {
		users[k] = v
	}
	usernames := make(map[string]domain.UserID, len(db.usernames))
	for k, v := range db.usernames {
		usernames[k] = v
	}
	sessions := make(map[domain.SessionID]domain.Session, len(db.sessions))
	for k, v := range db.sessions {
		sessions[k] = v
	}
	passwords := make(map[domain.UserID]domain.PasswordCredential, len(db.passwords))
	for k, v := range db.passwords {
		passwords[k] = v
	}
	configs := make(map[domain.ConfigVersionID]domain.ConfigVersion, len(db.configs))
	for k, v := range db.configs {
		configs[k] = v
	}
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	nodes := make(map[v1.NodeID]v1.Node, len(db.nodes))
	for k, v := range db.nodes {
		nodes[k] = v
	}
	parts := make(map[v1.PartID]v1.Part, len(db.parts))
	for k, v := range db.parts {
		parts[k] = v
	}
	relations := make(map[v1.RelationID]v1.Relation, len(db.relations))
	for k, v := range db.relations {
		relations[k] = v
	}
	bindings := make(map[v1.SourceBindingID]v1.SourceBinding, len(db.bindings))
	for k, v := range db.bindings {
		bindings[k] = v
	}
	moduleSettings := make(map[string]domain.ModuleSettings, len(db.moduleSettings))
	for k, v := range db.moduleSettings {
		moduleSettings[k] = v
	}
	// userPreferences was missing from this snapshot, which quietly meant a
	// rollback did not discard a preference write — a fake that cannot fail the
	// test it exists for. playbackStates joins it here rather than repeating it.
	userPreferences := make(map[string]domain.UserPreference, len(db.userPreferences))
	for k, v := range db.userPreferences {
		userPreferences[k] = v
	}
	playbackStates := make(map[playbackKey]v1.PlaybackState, len(db.playbackStates))
	for k, v := range db.playbackStates {
		playbackStates[k] = v
	}

	return fakeDBSnapshot{
		users:          users,
		usernames:      usernames,
		sessions:       sessions,
		passwords:      passwords,
		configs:        configs,
		outbox:         outbox,
		nodes:          nodes,
		parts:          parts,
		relations:      relations,
		bindings:       bindings,
		moduleSettings: moduleSettings,

		userPreferences: userPreferences,
		playbackStates:  playbackStates,
	}
}

func (db *fakeDB) restore(snap fakeDBSnapshot) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.users = snap.users
	db.usernames = snap.usernames
	db.sessions = snap.sessions
	db.passwords = snap.passwords
	db.configs = snap.configs
	db.outbox = snap.outbox
	db.nodes = snap.nodes
	db.parts = snap.parts
	db.relations = snap.relations
	db.bindings = snap.bindings
	db.moduleSettings = snap.moduleSettings
	db.userPreferences = snap.userPreferences
	db.playbackStates = snap.playbackStates
}

// fakeUserStore implements contracts.UserStore. It deliberately does not
// enforce username uniqueness itself — that is the application service's
// domain rule to enforce, not the store's.
type fakeUserStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeUserStore) Create(_ context.Context, user domain.User) (domain.User, error) {
	s.trace.record("users.create")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.users[user.ID] = user
	s.db.usernames[user.Username] = user.ID
	return user, nil
}

func (s *fakeUserStore) FindByID(_ context.Context, id domain.UserID) (domain.User, error) {
	s.trace.record("users.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	user, ok := s.db.users[id]
	if !ok {
		return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
	}
	return user, nil
}

func (s *fakeUserStore) FindByUsername(_ context.Context, username string) (domain.User, error) {
	s.trace.record("users.find_by_username")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	id, ok := s.db.usernames[username]
	if !ok {
		return domain.User{}, contracts.NewError(contracts.NotFound, "user not found")
	}
	return s.db.users[id], nil
}

func (s *fakeUserStore) Update(_ context.Context, user domain.User) (domain.User, error) {
	s.trace.record("users.update")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.users[user.ID] = user
	return user, nil
}

func (s *fakeUserStore) List(_ context.Context) ([]domain.User, error) {
	s.trace.record("users.list")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	users := make([]domain.User, 0, len(s.db.users))
	for _, user := range s.db.users {
		users = append(users, user)
	}
	sort.Slice(users, func(i, j int) bool { return users[i].CreatedAt.Before(users[j].CreatedAt) })
	return users, nil
}

// fakeSessionStore implements contracts.SessionStore.
type fakeSessionStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeSessionStore) Create(_ context.Context, session domain.Session) (domain.Session, error) {
	s.trace.record("sessions.create")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.sessions[session.ID] = session
	return session, nil
}

func (s *fakeSessionStore) FindByID(_ context.Context, id domain.SessionID) (domain.Session, error) {
	s.trace.record("sessions.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	session, ok := s.db.sessions[id]
	if !ok {
		return domain.Session{}, contracts.NewError(contracts.NotFound, "session not found")
	}
	return session, nil
}

func (s *fakeSessionStore) Revoke(_ context.Context, id domain.SessionID) error {
	s.trace.record("sessions.revoke")
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

// fakeCredentialStore implements contracts.CredentialStore. Only the
// password methods this slice's commands use are exercised by tests;
// passkey and recovery methods exist to satisfy the interface shape.
type fakeCredentialStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeCredentialStore) SavePassword(_ context.Context, credential domain.PasswordCredential) error {
	s.trace.record("credentials.save_password")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.passwords[credential.UserID] = credential
	return nil
}

func (s *fakeCredentialStore) FindPassword(_ context.Context, userID domain.UserID) (domain.PasswordCredential, error) {
	s.trace.record("credentials.find_password")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	credential, ok := s.db.passwords[userID]
	if !ok {
		return domain.PasswordCredential{}, contracts.NewError(contracts.NotFound, "password credential not found")
	}
	return credential, nil
}

func (s *fakeCredentialStore) SavePasskey(context.Context, domain.PasskeyCredential) error {
	return nil
}

func (s *fakeCredentialStore) ListPasskeys(context.Context, domain.UserID) ([]domain.PasskeyCredential, error) {
	return nil, nil
}

func (s *fakeCredentialStore) SaveRecoveryFactor(context.Context, domain.RecoveryFactor) error {
	return nil
}

func (s *fakeCredentialStore) ConsumeRecoveryFactor(context.Context, domain.UserID, string) (domain.RecoveryFactor, error) {
	return domain.RecoveryFactor{}, contracts.NewError(contracts.NotFound, "recovery factor not found")
}

// fakePermissionStore implements contracts.PermissionStore by returning
// whatever roles the test seeded for a user — the real ABAC-shaped
// policy.Engine (not a hardcoded stub) drives every allow/deny decision
// in these tests. RolesForUser is traced because it is the only
// observable signature of a real policy evaluation happening: the
// production policy.Engine itself does not record to trace.
type fakePermissionStore struct {
	db    *fakeDB
	trace *trace
}

func (s fakePermissionStore) RolesForUser(_ context.Context, userID domain.UserID) ([]domain.Role, error) {
	s.trace.record("permissions.roles_for_user")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	return append([]domain.Role(nil), s.db.roles[userID]...), nil
}

func (s fakePermissionStore) GrantsForUser(context.Context, domain.UserID) ([]domain.Grant, error) {
	return nil, nil
}

func (s fakePermissionStore) AttributesForUser(context.Context, domain.UserID) ([]domain.Attribute, error) {
	return nil, nil
}

func (s fakePermissionStore) CreateRole(_ context.Context, role domain.Role) (domain.Role, error) {
	s.trace.record("permissions.create_role")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, exists := s.db.rolesByID[role.ID]; exists {
		return domain.Role{}, contracts.NewError(contracts.Conflict, "role already exists")
	}
	s.db.rolesByID[role.ID] = role
	return role, nil
}

// SetRolePermissions replaces what a role carries, in both the catalogue and
// the per-user view — the fake's two copies of one row in the real schema.
// Updating only the catalogue would leave a reconciled role invisible to the
// policy engine, which reads through RolesForUser.
func (s fakePermissionStore) SetRolePermissions(_ context.Context, roleID domain.RoleID, perms []domain.Permission) error {
	s.trace.record("permissions.set_role_permissions")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	role, ok := s.db.rolesByID[roleID]
	if !ok {
		return contracts.NewError(contracts.NotFound, "role not found")
	}
	role.Permissions = append([]domain.Permission(nil), perms...)
	s.db.rolesByID[roleID] = role
	for user, roles := range s.db.roles {
		for i, held := range roles {
			if held.ID == roleID {
				s.db.roles[user][i] = role
			}
		}
	}
	return nil
}

func (s fakePermissionStore) GrantRole(_ context.Context, grant domain.Grant) error {
	s.trace.record("permissions.grant_role")
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

// fakeConfigStore implements contracts.ConfigStore against the shared
// fakeDB, mirroring fakeUserStore, so the config.Manager driving the
// Draft/Validate/Activate commands has somewhere real to persist to.
type fakeConfigStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeConfigStore) Save(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	s.trace.record("config.save")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.configs[version.ID] = version
	return version, nil
}

func (s *fakeConfigStore) Latest(_ context.Context) (domain.ConfigVersion, error) {
	s.trace.record("config.latest")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var latest domain.ConfigVersion
	found := false
	for _, v := range s.db.configs {
		if !found || v.CreatedAt.After(latest.CreatedAt) {
			latest = v
			found = true
		}
	}
	if !found {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
	}
	return latest, nil
}

func (s *fakeConfigStore) FindByID(_ context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error) {
	s.trace.record("config.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	version, ok := s.db.configs[id]
	if !ok {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
	}
	return version, nil
}

func (s *fakeConfigStore) FindActive(_ context.Context) (domain.ConfigVersion, error) {
	s.trace.record("config.find_active")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	for _, v := range s.db.configs {
		if v.Status == domain.ConfigActive {
			return v, nil
		}
	}
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no active config version")
}

func (s *fakeConfigStore) UpdateStatus(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	s.trace.record("config.update_status:" + string(version.Status))
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

// fakeEventOutbox implements contracts.EventOutbox. Trace entries include
// the event type so tests can assert which audit event committed, not
// merely that some event did.
type fakeEventOutbox struct {
	db    *fakeDB
	trace *trace
}

func (o *fakeEventOutbox) Append(_ context.Context, event domain.OutboxEvent) error {
	o.trace.record("outbox.append:" + event.Type)
	o.db.mu.Lock()
	defer o.db.mu.Unlock()
	o.db.outbox = append(o.db.outbox, event)
	return nil
}

func (o *fakeEventOutbox) ListUnpublished(context.Context, int) ([]domain.OutboxEvent, error) {
	o.db.mu.Lock()
	defer o.db.mu.Unlock()
	return append([]domain.OutboxEvent(nil), o.db.outbox...), nil
}

func (o *fakeEventOutbox) MarkPublished(context.Context, domain.EventID) error {
	return nil
}

func (o *fakeEventOutbox) RecordFailure(context.Context, domain.EventID, contracts.ErrorCategory, string) error {
	return nil
}

// fakeEventPublisher implements contracts.EventPublisher. It is used for
// audit events with no state change to bind atomicity to (authentication
// failures, authorization denials) — the real, transactional writes go
// through fakeEventOutbox instead.
type fakeEventPublisher struct {
	trace *trace
	mu    sync.Mutex
	sent  []domain.Event
}

func (p *fakeEventPublisher) Publish(_ context.Context, event domain.Event) error {
	p.trace.record("events.publish:" + event.Type)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sent = append(p.sent, event)
	return nil
}

func (p *fakeEventPublisher) Subscribe(string, contracts.EventHandler) (contracts.Subscription, error) {
	return fakeSubscription{}, nil
}

type fakeSubscription struct{}

func (fakeSubscription) Unsubscribe() {}

// fakeTx implements contracts.Tx. Every store it returns operates directly
// on the shared fakeDB, so writes made during WithinTx are immediately
// visible to fakeUnitOfWork's snapshot/restore bookkeeping.
type fakeTx struct {
	db    *fakeDB
	trace *trace
}

func (tx *fakeTx) Users() contracts.UserStore { return &fakeUserStore{db: tx.db, trace: tx.trace} }
func (tx *fakeTx) Sessions() contracts.SessionStore {
	return &fakeSessionStore{db: tx.db, trace: tx.trace}
}
func (tx *fakeTx) Permissions() contracts.PermissionStore {
	return fakePermissionStore{db: tx.db, trace: tx.trace}
}
func (tx *fakeTx) Config() contracts.ConfigStore { return &fakeConfigStore{db: tx.db, trace: tx.trace} }
func (tx *fakeTx) Outbox() contracts.EventOutbox { return &fakeEventOutbox{db: tx.db, trace: tx.trace} }
func (tx *fakeTx) Credentials() contracts.CredentialStore {
	return &fakeCredentialStore{db: tx.db, trace: tx.trace}
}

func (tx *fakeTx) Nodes() contracts.NodeStore { return &fakeNodeStore{db: tx.db, trace: tx.trace} }
func (tx *fakeTx) Parts() contracts.PartStore { return &fakePartStore{db: tx.db, trace: tx.trace} }

func (tx *fakeTx) Relations() contracts.RelationStore {
	return &fakeRelationStore{db: tx.db, trace: tx.trace}
}
func (tx *fakeTx) SourceBindings() contracts.SourceBindingStore {
	return &fakeSourceBindingStore{db: tx.db, trace: tx.trace}
}
func (tx *fakeTx) ModuleSettings() contracts.ModuleSettingsStore {
	return &fakeModuleSettingsStore{db: tx.db, trace: tx.trace}
}
func (tx *fakeTx) UserPreferences() contracts.UserPreferenceStore {
	return &fakeUserPreferenceStore{db: tx.db, trace: tx.trace}
}
func (tx *fakeTx) PlaybackStates() contracts.PlaybackStateStore {
	return &fakePlaybackStateStore{db: tx.db, trace: tx.trace}
}

// InstalledExtensions is nil here: no application service reads the installed set
// yet (boot re-adoption reads it directly through the pool, not a Tx), so no test
// through this fake exercises it. It exists to satisfy contracts.Tx.
func (tx *fakeTx) InstalledExtensions() contracts.InstalledExtensionStore { return nil }

// fakeUserPreferenceStore implements contracts.UserPreferenceStore over
// fakeDB, keyed the same way the real table is: one entry per (user, key).
type fakeUserPreferenceStore struct {
	db    *fakeDB
	trace *trace
}

func prefKey(userID domain.UserID, key string) string { return string(userID) + "\u0000" + key }

func (s *fakeUserPreferenceStore) Get(_ context.Context, userID domain.UserID, key string) (domain.UserPreference, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	pref, ok := s.db.userPreferences[prefKey(userID, key)]
	if !ok {
		return domain.UserPreference{}, contracts.NewError(contracts.NotFound, "preference not set")
	}
	return pref, nil
}

func (s *fakeUserPreferenceStore) List(_ context.Context, userID domain.UserID) ([]domain.UserPreference, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var out []domain.UserPreference
	for _, pref := range s.db.userPreferences {
		if pref.UserID == userID {
			out = append(out, pref)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, nil
}

func (s *fakeUserPreferenceStore) Set(_ context.Context, pref domain.UserPreference) (domain.UserPreference, error) {
	s.trace.record("user_preferences.set:" + pref.Key)
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.userPreferences[prefKey(pref.UserID, pref.Key)] = pref
	return pref, nil
}

func (s *fakeUserPreferenceStore) Delete(_ context.Context, userID domain.UserID, key string) error {
	s.trace.record("user_preferences.delete:" + key)
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	k := prefKey(userID, key)
	if _, ok := s.db.userPreferences[k]; !ok {
		return contracts.NewError(contracts.NotFound, "preference not set")
	}
	delete(s.db.userPreferences, k)
	return nil
}

// fakeModuleSettingsStore implements contracts.ModuleSettingsStore over
// fakeDB, backing both the direct read handle and the fakeTx write path.
type fakeModuleSettingsStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeModuleSettingsStore) Get(_ context.Context, moduleID string) (domain.ModuleSettings, error) {
	s.trace.record("module_settings.get:" + moduleID)
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if ms, ok := s.db.moduleSettings[moduleID]; ok {
		return ms, nil
	}
	return domain.ModuleSettings{ModuleID: moduleID, Settings: []byte("{}")}, nil
}

func (s *fakeModuleSettingsStore) Set(_ context.Context, ms domain.ModuleSettings) (domain.ModuleSettings, error) {
	s.trace.record("module_settings.set:" + ms.ModuleID)
	if len(ms.Settings) == 0 {
		ms.Settings = []byte("{}")
	}
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.moduleSettings[ms.ModuleID] = ms
	return ms, nil
}

// fakeUnitOfWork implements contracts.UnitOfWork with real rollback
// semantics: it snapshots the shared fakeDB before calling fn, and
// restores that snapshot if fn returns an error, so a test can prove
// state and outbox events commit together or not at all.
type fakeUnitOfWork struct {
	db    *fakeDB
	trace *trace
}

func (u *fakeUnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context, tx contracts.Tx) error) error {
	u.trace.record("uow.begin")
	snap := u.db.snapshot()

	tx := &fakeTx{db: u.db, trace: u.trace}
	if err := fn(ctx, tx); err != nil {
		u.db.restore(snap)
		u.trace.record("uow.rolled_back")
		return err
	}

	u.trace.record("uow.committed")
	return nil
}

// fakeClock implements contracts.Clock with a fixed time.
type fakeClock struct {
	now time.Time
}

func (c fakeClock) Now() time.Time { return c.now }

// fakeIDGenerator implements contracts.IDGenerator with sequential,
// deterministic IDs.
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

// fakePasswordVerifier implements domain.PasswordVerifier with a
// reversible, deliberately insecure stand-in. Real hashing (Argon2id)
// belongs to a future crypto adapter; this exists purely
// to exercise the interface boundary in tests.
type fakePasswordVerifier struct{}

func (fakePasswordVerifier) Hash(plaintext string) (string, error) {
	return "insecure-test-hash:" + plaintext, nil
}

func (fakePasswordVerifier) Verify(plaintext string, hash string) (bool, error) {
	return hash == "insecure-test-hash:"+plaintext, nil
}

func newTestService(db *fakeDB, tr *trace, now time.Time) *app.Service {
	return newTestServiceWithCapabilities(db, tr, now, nil)
}

// newTestServiceWithCapabilities is newTestService with a capability registry,
// for the ImportContent path. Most tests register nothing and use the wrapper
// above.
func newTestServiceWithCapabilities(db *fakeDB, tr *trace, now time.Time, caps *app.CapabilityRegistry) *app.Service {
	d := baseTestDeps(db, tr, now)
	d.Capabilities = caps
	return app.NewService(d)
}

// newTestServiceWithExtensions is newTestService with an injected extension
// manager, for the install/uninstall path.
func newTestServiceWithExtensions(db *fakeDB, tr *trace, now time.Time, ext app.ExtensionManager) *app.Service {
	d := baseTestDeps(db, tr, now)
	d.Extensions = ext
	return app.NewService(d)
}

// baseTestDeps is the common fake wiring both variants share, so a new
// dependency is added in one place rather than copied per helper.
func baseTestDeps(db *fakeDB, tr *trace, now time.Time) app.Deps {
	return app.Deps{
		UnitOfWork:       &fakeUnitOfWork{db: db, trace: tr},
		Sessions:         &fakeSessionStore{db: db, trace: tr},
		Users:            &fakeUserStore{db: db, trace: tr},
		Credentials:      &fakeCredentialStore{db: db, trace: tr},
		Config:           &fakeConfigStore{db: db, trace: tr},
		Permissions:      fakePermissionStore{db: db, trace: tr},
		Nodes:            &fakeNodeStore{db: db, trace: tr},
		Parts:            &fakePartStore{db: db, trace: tr},
		Clock:            fakeClock{now: now},
		IDs:              &fakeIDGenerator{},
		ContentIDs:       &fakeIDGenerator{},
		Policy:           policy.NewEngine(fakePermissionStore{db: db, trace: tr}),
		Events:           &fakeEventPublisher{trace: tr},
		PasswordVerifier: fakePasswordVerifier{},
		ModuleSettings:   &fakeModuleSettingsStore{db: db, trace: tr},
		UserPreferences:  &fakeUserPreferenceStore{db: db, trace: tr},
		PlaybackStates:   &fakePlaybackStateStore{db: db, trace: tr},
		TelemetryQueries: fakeTelemetryQueryStore{},
	}
}

// fakeNodeStore implements contracts.NodeStore over fakeDB. The reads the
// content query services make are implemented faithfully — including the
// canonicalisation ADR 0015 requires of any implementation — so an app-level
// test proves the service, not the adapter.
type fakeNodeStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeNodeStore) Create(_ context.Context, node v1.Node) (v1.Node, error) {
	s.trace.record("nodes.create")
	node = node.Canonical()
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	s.db.nodes[node.ID] = node
	return node, nil
}

func (s *fakeNodeStore) FindByID(_ context.Context, id v1.NodeID) (v1.Node, error) {
	s.trace.record("nodes.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	node, ok := s.db.nodes[id]
	if !ok {
		return v1.Node{}, contracts.NewError(contracts.NotFound, "node not found")
	}
	return node, nil
}

func (s *fakeNodeStore) Update(_ context.Context, node v1.Node) (v1.Node, error) {
	s.trace.record("nodes.update")
	node = node.Canonical()
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.nodes[node.ID]; !ok {
		return v1.Node{}, contracts.NewError(contracts.NotFound, "node not found")
	}
	s.db.nodes[node.ID] = node
	return node, nil
}

func (s *fakeNodeStore) ListChildren(_ context.Context, parentID v1.NodeID) ([]v1.Node, error) {
	s.trace.record("nodes.list_children")
	return s.collect(func(n v1.Node) bool {
		return n.ParentID != nil && *n.ParentID == parentID
	}, byNaturalOrder), nil
}

func (s *fakeNodeStore) ListByWork(_ context.Context, workID v1.NodeID) ([]v1.Node, error) {
	s.trace.record("nodes.list_by_work")
	return s.collect(func(n v1.Node) bool { return n.WorkID == workID }, byNaturalOrder), nil
}

func (s *fakeNodeStore) ListWorks(_ context.Context, mediaType v1.MediaType) ([]v1.Node, error) {
	s.trace.record("nodes.list_works")
	want := v1.NormaliseMediaType(string(mediaType))
	return s.collect(func(n v1.Node) bool {
		return n.ParentID == nil && (want == "" || n.MediaType == want)
	}, byTitle), nil
}

func (s *fakeNodeStore) Search(_ context.Context, query contracts.NodeQuery) ([]v1.Node, error) {
	s.trace.record("nodes.search")
	if query.Limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	title := strings.ToLower(query.Title)
	wantMedia := v1.NormaliseMediaType(string(query.MediaType))

	found := s.collect(func(n v1.Node) bool {
		if title != "" && !strings.Contains(strings.ToLower(n.Title), title) {
			return false
		}
		if wantMedia != "" && n.MediaType != wantMedia {
			return false
		}
		if query.Kind != "" && n.Kind != query.Kind {
			return false
		}
		return true
	}, byTitle)

	if len(found) > query.Limit {
		found = found[:query.Limit]
	}
	return found, nil
}

func (s *fakeNodeStore) FindByExternalID(_ context.Context, scheme, value string) ([]v1.Node, error) {
	s.trace.record("nodes.find_by_external_id")
	if scheme == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "external id scheme is required")
	}
	if value == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "external id value is required")
	}
	return s.collect(func(n v1.Node) bool {
		ids := map[string]string{}
		if err := json.Unmarshal(n.ExternalIDs, &ids); err != nil {
			// An unparseable document simply does not match, which is what
			// jsonb containment does too.
			return false
		}
		return ids[scheme] == value
	}, byTitle), nil
}

func (s *fakeNodeStore) Delete(_ context.Context, id v1.NodeID) error {
	s.trace.record("nodes.delete")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.nodes[id]; !ok {
		return contracts.NewError(contracts.NotFound, "node not found")
	}
	delete(s.db.nodes, id)
	return nil
}

func (s *fakeNodeStore) collect(match func(v1.Node) bool, less func(a, b v1.Node) bool) []v1.Node {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var found []v1.Node
	for _, node := range s.db.nodes {
		if match(node) {
			found = append(found, node)
		}
	}
	sort.Slice(found, func(i, j int) bool { return less(found[i], found[j]) })
	return found
}

func byTitle(a, b v1.Node) bool {
	if a.Title != b.Title {
		return a.Title < b.Title
	}
	return a.ID < b.ID
}

func byNaturalOrder(a, b v1.Node) bool {
	if a.NaturalOrder != b.NaturalOrder {
		return a.NaturalOrder < b.NaturalOrder
	}
	return a.ID < b.ID
}

func (db *fakeDB) seedNode(node v1.Node) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.nodes[node.ID] = node.Canonical()
}

// fakePartStore implements contracts.PartStore over fakeDB, enough for the
// content commands: it enforces the node-is-item rule the real adapter's
// foreign key does, so a command test sees the same InvalidArgument.
type fakePartStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakePartStore) Create(_ context.Context, part v1.Part) (v1.Part, error) {
	s.trace.record("parts.create")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	node, ok := s.db.nodes[part.NodeID]
	if !ok || node.Kind != v1.NodeItem {
		return v1.Part{}, contracts.NewError(contracts.InvalidArgument, "part must attach to an existing item node")
	}
	s.db.parts[part.ID] = part
	return part, nil
}

func (s *fakePartStore) FindByID(_ context.Context, id v1.PartID) (v1.Part, error) {
	s.trace.record("parts.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	part, ok := s.db.parts[id]
	if !ok {
		return v1.Part{}, contracts.NewError(contracts.NotFound, "part not found")
	}
	return part, nil
}

func (s *fakePartStore) Update(_ context.Context, part v1.Part) (v1.Part, error) {
	s.trace.record("parts.update")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.parts[part.ID]; !ok {
		return v1.Part{}, contracts.NewError(contracts.NotFound, "part not found")
	}
	s.db.parts[part.ID] = part
	return part, nil
}

func (s *fakePartStore) ListByNode(_ context.Context, nodeID v1.NodeID) ([]v1.Part, error) {
	s.trace.record("parts.list_by_node")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var parts []v1.Part
	for _, p := range s.db.parts {
		if p.NodeID == nodeID {
			parts = append(parts, p)
		}
	}
	sort.Slice(parts, func(i, j int) bool {
		if parts[i].NaturalOrder != parts[j].NaturalOrder {
			return parts[i].NaturalOrder < parts[j].NaturalOrder
		}
		return parts[i].ID < parts[j].ID
	})
	return parts, nil
}

func (s *fakePartStore) Delete(_ context.Context, id v1.PartID) error {
	s.trace.record("parts.delete")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.parts[id]; !ok {
		return contracts.NewError(contracts.NotFound, "part not found")
	}
	delete(s.db.parts, id)
	return nil
}

// outboxTypes returns the event types appended to the outbox, for assertions
// that a command emitted (or, on rollback, did not emit) its event.
func (db *fakeDB) outboxTypes() []string {
	db.mu.Lock()
	defer db.mu.Unlock()
	types := make([]string, len(db.outbox))
	for i, e := range db.outbox {
		types[i] = e.Type
	}
	return types
}

func (db *fakeDB) outboxHas(eventType string) bool {
	for _, t := range db.outboxTypes() {
		if t == eventType {
			return true
		}
	}
	return false
}

// fakeRelationStore implements contracts.RelationStore over fakeDB, enforcing
// the unique-edge rule the real adapter's constraint does.
type fakeRelationStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeRelationStore) Create(_ context.Context, relation v1.Relation) (v1.Relation, error) {
	s.trace.record("relations.create")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	for _, existing := range s.db.relations {
		if existing.FromNodeID == relation.FromNodeID &&
			existing.ToNodeID == relation.ToNodeID &&
			existing.Type == relation.Type {
			return v1.Relation{}, contracts.NewError(contracts.Conflict, "relation already exists")
		}
	}
	s.db.relations[relation.ID] = relation
	return relation, nil
}

func (s *fakeRelationStore) FindByID(_ context.Context, id v1.RelationID) (v1.Relation, error) {
	s.trace.record("relations.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	relation, ok := s.db.relations[id]
	if !ok {
		return v1.Relation{}, contracts.NewError(contracts.NotFound, "relation not found")
	}
	return relation, nil
}

func (s *fakeRelationStore) ListFrom(_ context.Context, from v1.NodeID, relationType v1.RelationType) ([]v1.Relation, error) {
	s.trace.record("relations.list_from")
	return s.list(func(r v1.Relation) bool {
		return r.FromNodeID == from && (relationType == "" || r.Type == relationType)
	}), nil
}

func (s *fakeRelationStore) ListTo(_ context.Context, to v1.NodeID, relationType v1.RelationType) ([]v1.Relation, error) {
	s.trace.record("relations.list_to")
	return s.list(func(r v1.Relation) bool {
		return r.ToNodeID == to && (relationType == "" || r.Type == relationType)
	}), nil
}

func (s *fakeRelationStore) Delete(_ context.Context, id v1.RelationID) error {
	s.trace.record("relations.delete")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.relations[id]; !ok {
		return contracts.NewError(contracts.NotFound, "relation not found")
	}
	delete(s.db.relations, id)
	return nil
}

func (s *fakeRelationStore) list(match func(v1.Relation) bool) []v1.Relation {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var found []v1.Relation
	for _, r := range s.db.relations {
		if match(r) {
			found = append(found, r)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found
}

// fakeSourceBindingStore implements contracts.SourceBindingStore over fakeDB,
// enforcing the one-source-one-node uniqueness the real adapter's constraint
// does.
type fakeSourceBindingStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakeSourceBindingStore) Create(_ context.Context, binding v1.SourceBinding) (v1.SourceBinding, error) {
	s.trace.record("bindings.create")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	for _, existing := range s.db.bindings {
		if existing.SourceProvider == binding.SourceProvider && existing.SourceRef == binding.SourceRef {
			return v1.SourceBinding{}, contracts.NewError(contracts.Conflict, "source already bound")
		}
	}
	s.db.bindings[binding.ID] = binding
	return binding, nil
}

func (s *fakeSourceBindingStore) FindByID(_ context.Context, id v1.SourceBindingID) (v1.SourceBinding, error) {
	s.trace.record("bindings.find_by_id")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	binding, ok := s.db.bindings[id]
	if !ok {
		return v1.SourceBinding{}, contracts.NewError(contracts.NotFound, "source binding not found")
	}
	return binding, nil
}

func (s *fakeSourceBindingStore) FindBySource(_ context.Context, provider, ref string) (v1.SourceBinding, error) {
	s.trace.record("bindings.find_by_source")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	for _, b := range s.db.bindings {
		if b.SourceProvider == provider && b.SourceRef == ref {
			return b, nil
		}
	}
	return v1.SourceBinding{}, contracts.NewError(contracts.NotFound, "source binding not found")
}

func (s *fakeSourceBindingStore) Update(_ context.Context, binding v1.SourceBinding) (v1.SourceBinding, error) {
	s.trace.record("bindings.update")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.bindings[binding.ID]; !ok {
		return v1.SourceBinding{}, contracts.NewError(contracts.NotFound, "source binding not found")
	}
	s.db.bindings[binding.ID] = binding
	return binding, nil
}

func (s *fakeSourceBindingStore) ListByNode(_ context.Context, nodeID v1.NodeID) ([]v1.SourceBinding, error) {
	s.trace.record("bindings.list_by_node")
	return s.list(func(b v1.SourceBinding) bool { return b.NodeID == nodeID }), nil
}

func (s *fakeSourceBindingStore) ListPendingReview(_ context.Context, limit int) ([]v1.SourceBinding, error) {
	s.trace.record("bindings.list_pending")
	if limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}
	found := s.list(func(b v1.SourceBinding) bool { return b.Status == v1.BindingPendingReview })
	if len(found) > limit {
		found = found[:limit]
	}
	return found, nil
}

func (s *fakeSourceBindingStore) Delete(_ context.Context, id v1.SourceBindingID) error {
	s.trace.record("bindings.delete")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if _, ok := s.db.bindings[id]; !ok {
		return contracts.NewError(contracts.NotFound, "source binding not found")
	}
	delete(s.db.bindings, id)
	return nil
}

func (s *fakeSourceBindingStore) list(match func(v1.SourceBinding) bool) []v1.SourceBinding {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var found []v1.SourceBinding
	for _, b := range s.db.bindings {
		if match(b) {
			found = append(found, b)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].ID < found[j].ID })
	return found
}

// seedPart puts a Part in place without going through the command path, so a
// read test can describe the library state it needs rather than build it.
func (db *fakeDB) seedPart(part v1.Part) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.parts[part.ID] = part
}

// FindRole resolves a role across every user's roles, mirroring the real
// store's id lookup. The delegation check (ADR 0069) reads through it, so it
// has to return the role's real permissions rather than a stub.
func (s fakePermissionStore) FindRole(_ context.Context, roleID domain.RoleID) (domain.Role, error) {
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	// rolesByID, not the per-user map: a role exists once in the real schema
	// regardless of who holds it, and the delegation check has to be able to
	// see a role nobody has been granted yet.
	role, ok := s.db.rolesByID[roleID]
	if !ok {
		return domain.Role{}, contracts.NewError(contracts.NotFound, "no role with that id")
	}
	return role, nil
}

// fakePlaybackStateStore implements contracts.PlaybackStateStore over fakeDB
// (ADR 0046). It reproduces the two behaviours the real store's SQL encodes and
// a caller depends on: NotFound for a node never started, and an in-progress
// list that excludes both finished items and items opened at position zero.
type fakePlaybackStateStore struct {
	db    *fakeDB
	trace *trace
}

func (s *fakePlaybackStateStore) Get(_ context.Context, userID domain.UserID, nodeID v1.NodeID) (v1.PlaybackState, error) {
	s.trace.record("playback.get")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	state, ok := s.db.playbackStates[playbackKey{user: userID, node: nodeID}]
	if !ok {
		return v1.PlaybackState{}, contracts.NewError(contracts.NotFound, "no playback state for this item")
	}
	return state, nil
}

func (s *fakePlaybackStateStore) ListByNodes(_ context.Context, userID domain.UserID, nodeIDs []v1.NodeID) (map[v1.NodeID]v1.PlaybackState, error) {
	s.trace.record("playback.listByNodes")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	out := map[v1.NodeID]v1.PlaybackState{}
	for _, id := range nodeIDs {
		if state, ok := s.db.playbackStates[playbackKey{user: userID, node: id}]; ok {
			out[id] = state
		}
	}
	return out, nil
}

func (s *fakePlaybackStateStore) ListInProgress(_ context.Context, userID domain.UserID, limit int) ([]v1.PlaybackState, error) {
	s.trace.record("playback.listInProgress")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	var out []v1.PlaybackState
	for key, state := range s.db.playbackStates {
		if key.user != userID || !state.InProgress() {
			continue
		}
		out = append(out, state)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].UpdatedAt.After(out[j].UpdatedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakePlaybackStateStore) Upsert(_ context.Context, userID domain.UserID, state v1.PlaybackState) (v1.PlaybackState, error) {
	s.trace.record("playback.upsert")
	s.db.mu.Lock()
	defer s.db.mu.Unlock()
	if s.db.playbackStates == nil {
		s.db.playbackStates = map[playbackKey]v1.PlaybackState{}
	}
	s.db.playbackStates[playbackKey{user: userID, node: state.NodeID}] = state
	return state, nil
}
