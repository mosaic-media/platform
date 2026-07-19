package app_test

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// trace records the order in which contract and policy boundary calls
// occur, so tests can assert the MEG-015 §04 command/query order actually
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
	users     map[domain.UserID]domain.User
	usernames map[string]domain.UserID
	sessions  map[domain.SessionID]domain.Session
	passwords map[domain.UserID]domain.PasswordCredential
	configs   map[domain.ConfigVersionID]domain.ConfigVersion
	outbox    []domain.OutboxEvent
	nodes     map[v1.NodeID]v1.Node
	parts     map[v1.PartID]v1.Part
	relations map[v1.RelationID]v1.Relation
	bindings  map[v1.SourceBindingID]v1.SourceBinding
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
	// a fixture the tests seed directly, standing in for the "admin-
	// controlled permission assignment" MEG-015 §07 lists as in scope but
	// this slice does not build a command for.
	roles map[domain.UserID][]domain.Role

	// nodes and parts back the content commands and queries; relations and
	// bindings back the graph and identity commands.
	nodes     map[v1.NodeID]v1.Node
	parts     map[v1.PartID]v1.Part
	relations map[v1.RelationID]v1.Relation
	bindings  map[v1.SourceBindingID]v1.SourceBinding
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:     make(map[domain.UserID]domain.User),
		usernames: make(map[string]domain.UserID),
		sessions:  make(map[domain.SessionID]domain.Session),
		passwords: make(map[domain.UserID]domain.PasswordCredential),
		configs:   make(map[domain.ConfigVersionID]domain.ConfigVersion),
		roles:     make(map[domain.UserID][]domain.Role),
		nodes:     make(map[v1.NodeID]v1.Node),
		parts:     make(map[v1.PartID]v1.Part),
		relations: make(map[v1.RelationID]v1.Relation),
		bindings:  make(map[v1.SourceBindingID]v1.SourceBinding),
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
}

// adminRole grants every action this slice's commands check. Tests that
// need an authorized caller seed it for that caller's user ID; tests
// proving the policy gate simply don't.
func adminRole() domain.Role {
	return domain.Role{
		ID:   "role-admin",
		Name: "Administrator",
		Permissions: []domain.Permission{
			domain.Permission(app.ActionUserCreate),
			domain.Permission(app.ActionUserRead),
			domain.Permission(app.ActionSessionCreate),
			domain.Permission(app.ActionSessionRevoke),
			domain.Permission(app.ActionConfigDraft),
			domain.Permission(app.ActionConfigValidate),
			domain.Permission(app.ActionConfigActivate),
			domain.Permission(app.ActionUserList),
			domain.Permission(app.ActionUserStatusUpdate),
			domain.Permission(app.ActionPermissionRead),
			domain.Permission(app.ActionConfigRead),
			domain.Permission(app.ActionContentRead),
			domain.Permission(app.ActionContentCreate),
			domain.Permission(app.ActionContentRelate),
			domain.Permission(app.ActionContentBind),
			domain.Permission(app.ActionContentResolve),
		},
	}
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

	return fakeDBSnapshot{
		users:     users,
		usernames: usernames,
		sessions:  sessions,
		passwords: passwords,
		configs:   configs,
		outbox:    outbox,
		nodes:     nodes,
		parts:     parts,
		relations: relations,
		bindings:  bindings,
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
}

// fakeUserStore implements contracts.UserStore. It deliberately does not
// enforce username uniqueness itself — that is the application service's
// domain rule to enforce (MEG-015 §04 step 6), not the store's.
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
// passkey and recovery methods exist to satisfy the interface shape
// (MEG-015 §07 lists them as "modeled," not exercised, this slice).
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
// belongs to a future crypto adapter (MEG-015 §07); this exists purely
// to exercise the interface boundary in tests.
type fakePasswordVerifier struct{}

func (fakePasswordVerifier) Hash(plaintext string) (string, error) {
	return "insecure-test-hash:" + plaintext, nil
}

func (fakePasswordVerifier) Verify(plaintext string, hash string) (bool, error) {
	return hash == "insecure-test-hash:"+plaintext, nil
}

func newTestService(db *fakeDB, tr *trace, now time.Time) *app.Service {
	return app.NewService(
		&fakeUnitOfWork{db: db, trace: tr},
		&fakeSessionStore{db: db, trace: tr},
		&fakeUserStore{db: db, trace: tr},
		&fakeCredentialStore{db: db, trace: tr},
		&fakeConfigStore{db: db, trace: tr},
		fakePermissionStore{db: db, trace: tr},
		&fakeNodeStore{db: db, trace: tr},
		fakeClock{now: now},
		&fakeIDGenerator{},
		&fakeIDGenerator{},
		policy.NewEngine(fakePermissionStore{db: db, trace: tr}),
		&fakeEventPublisher{trace: tr},
		fakePasswordVerifier{},
	)
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
