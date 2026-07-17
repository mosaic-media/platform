package app_test

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
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

// fakeDB is the shared backing store behind every fake contract in this
// package. The same data is reachable directly (for authentication and
// query reads) and through a fakeTx (for the command write path), mirroring
// how a real adapter would expose one database through several contracts.
type fakeDB struct {
	mu        sync.Mutex
	users     map[domain.UserID]domain.User
	usernames map[string]domain.UserID
	sessions  map[domain.SessionID]domain.Session
	outbox    []domain.OutboxEvent
}

func newFakeDB() *fakeDB {
	return &fakeDB{
		users:     make(map[domain.UserID]domain.User),
		usernames: make(map[string]domain.UserID),
		sessions:  make(map[domain.SessionID]domain.Session),
	}
}

func (db *fakeDB) seedSession(id domain.SessionID, userID domain.UserID, now time.Time) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.sessions[id] = domain.Session{
		ID:        id,
		UserID:    userID,
		IssuedAt:  now.Add(-time.Hour),
		ExpiresAt: now.Add(time.Hour),
	}
}

func (db *fakeDB) seedUser(user domain.User) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.users[user.ID] = user
	db.usernames[user.Username] = user.ID
}

func (db *fakeDB) snapshot() (map[domain.UserID]domain.User, map[string]domain.UserID, []domain.OutboxEvent) {
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
	outbox := append([]domain.OutboxEvent(nil), db.outbox...)
	return users, usernames, outbox
}

func (db *fakeDB) restore(users map[domain.UserID]domain.User, usernames map[string]domain.UserID, outbox []domain.OutboxEvent) {
	db.mu.Lock()
	defer db.mu.Unlock()
	db.users = users
	db.usernames = usernames
	db.outbox = outbox
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

// fakePermissionStore and fakeConfigStore satisfy the remaining contracts.Tx
// stores. Neither command or query in this slice uses them; they exist
// only because fakeTx must implement the full contracts.Tx interface.
type fakePermissionStore struct{}

func (fakePermissionStore) RolesForUser(context.Context, domain.UserID) ([]domain.Role, error) {
	return nil, nil
}

func (fakePermissionStore) GrantsForUser(context.Context, domain.UserID) ([]domain.Grant, error) {
	return nil, nil
}

func (fakePermissionStore) AttributesForUser(context.Context, domain.UserID) ([]domain.Attribute, error) {
	return nil, nil
}

type fakeConfigStore struct{}

func (fakeConfigStore) Save(_ context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	return version, nil
}

func (fakeConfigStore) Latest(context.Context) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
}

func (fakeConfigStore) FindByID(context.Context, domain.ConfigVersionID) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
}

// fakeEventOutbox implements contracts.EventOutbox.
type fakeEventOutbox struct {
	db    *fakeDB
	trace *trace
}

func (o *fakeEventOutbox) Append(_ context.Context, event domain.OutboxEvent) error {
	o.trace.record("outbox.append")
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

// fakeTx implements contracts.Tx. Every store it returns operates directly
// on the shared fakeDB, so writes made during WithinTx are immediately
// visible to fakeUnitOfWork's snapshot/restore bookkeeping.
type fakeTx struct {
	db    *fakeDB
	trace *trace
}

func (tx *fakeTx) Users() contracts.UserStore       { return &fakeUserStore{db: tx.db, trace: tx.trace} }
func (tx *fakeTx) Sessions() contracts.SessionStore { return &fakeSessionStore{db: tx.db, trace: tx.trace} }
func (tx *fakeTx) Permissions() contracts.PermissionStore { return fakePermissionStore{} }
func (tx *fakeTx) Config() contracts.ConfigStore           { return fakeConfigStore{} }
func (tx *fakeTx) Outbox() contracts.EventOutbox           { return &fakeEventOutbox{db: tx.db, trace: tx.trace} }

// fakeUnitOfWork implements contracts.UnitOfWork with real rollback
// semantics: it snapshots the shared fakeDB before calling fn, and restores
// that snapshot if fn returns an error, so a test can prove state and
// outbox events commit together or not at all.
type fakeUnitOfWork struct {
	db    *fakeDB
	trace *trace
}

func (u *fakeUnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context, tx contracts.Tx) error) error {
	u.trace.record("uow.begin")
	users, usernames, outbox := u.db.snapshot()

	tx := &fakeTx{db: u.db, trace: u.trace}
	if err := fn(ctx, tx); err != nil {
		u.db.restore(users, usernames, outbox)
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

// fakePolicy implements app.PolicyDecisionPoint. It records that a
// decision was requested and returns a fixed, configurable verdict, so
// tests can exercise both the allow and deny paths through the same
// service.
type fakePolicy struct {
	trace *trace
	allow bool
}

func (p *fakePolicy) Authorize(_ context.Context, _ app.Subject, _ app.Action, _ app.Resource, _ app.PolicyContext) (app.Decision, error) {
	p.trace.record("policy.authorize")
	if p.allow {
		return app.Decision{Allowed: true, Reason: "fake policy: allow"}, nil
	}
	return app.Decision{Allowed: false, Reason: "fake policy: deny"}, nil
}

func newTestService(db *fakeDB, tr *trace, now time.Time, allow bool) *app.Service {
	return app.NewService(
		&fakeUnitOfWork{db: db, trace: tr},
		&fakeSessionStore{db: db, trace: tr},
		&fakeUserStore{db: db, trace: tr},
		fakeClock{now: now},
		&fakeIDGenerator{},
		&fakePolicy{trace: tr, allow: allow},
	)
}
