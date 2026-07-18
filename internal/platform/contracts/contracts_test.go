package contracts_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// The types and var blocks below exist solely to prove, at compile time,
// that each interface in the first contract set (MEG-015 §03) is
// implementable using only domain value types. There is no runtime
// behaviour to assert; a failed build is the test failure.

type mockUnitOfWork struct{}

func (mockUnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context, tx contracts.Tx) error) error {
	return fn(ctx, newMockTx())
}

// mockTx carries one instance of each store as a field so that the named
// accessors and Store[T] resolution both hand back the identical instance,
// making the equivalence test below provable by pointer identity rather than
// by empty-struct value equality (every mockUserStore{} would otherwise
// compare equal). Store[T] resolves by delegating to these same accessors, so
// mockTx satisfies resolution in addition to its six existing methods without
// any new method.
type mockTx struct {
	users       *mockUserStore
	sessions    *mockSessionStore
	permissions *mockPermissionStore
	config      *mockConfigStore
	outbox      *mockEventOutbox
	credentials *mockCredentialStore
}

func newMockTx() mockTx {
	return mockTx{
		users:       &mockUserStore{},
		sessions:    &mockSessionStore{},
		permissions: &mockPermissionStore{},
		config:      &mockConfigStore{},
		outbox:      &mockEventOutbox{},
		credentials: &mockCredentialStore{},
	}
}

func (tx mockTx) Users() contracts.UserStore             { return tx.users }
func (tx mockTx) Sessions() contracts.SessionStore       { return tx.sessions }
func (tx mockTx) Permissions() contracts.PermissionStore { return tx.permissions }
func (tx mockTx) Config() contracts.ConfigStore          { return tx.config }
func (tx mockTx) Outbox() contracts.EventOutbox          { return tx.outbox }
func (tx mockTx) Credentials() contracts.CredentialStore { return tx.credentials }

type mockUserStore struct{}

func (mockUserStore) Create(ctx context.Context, user domain.User) (domain.User, error) {
	return user, nil
}

func (mockUserStore) FindByID(ctx context.Context, id domain.UserID) (domain.User, error) {
	return domain.User{}, nil
}

func (mockUserStore) FindByUsername(ctx context.Context, username string) (domain.User, error) {
	return domain.User{}, nil
}

func (mockUserStore) Update(ctx context.Context, user domain.User) (domain.User, error) {
	return user, nil
}

func (mockUserStore) List(ctx context.Context) ([]domain.User, error) {
	return nil, nil
}

type mockSessionStore struct{}

func (mockSessionStore) Create(ctx context.Context, session domain.Session) (domain.Session, error) {
	return session, nil
}

func (mockSessionStore) FindByID(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	return domain.Session{}, nil
}

func (mockSessionStore) Revoke(ctx context.Context, id domain.SessionID) error {
	return nil
}

type mockPermissionStore struct{}

func (mockPermissionStore) RolesForUser(ctx context.Context, userID domain.UserID) ([]domain.Role, error) {
	return nil, nil
}

func (mockPermissionStore) GrantsForUser(ctx context.Context, userID domain.UserID) ([]domain.Grant, error) {
	return nil, nil
}

func (mockPermissionStore) AttributesForUser(ctx context.Context, userID domain.UserID) ([]domain.Attribute, error) {
	return nil, nil
}

type mockConfigStore struct{}

func (mockConfigStore) Save(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	return version, nil
}

func (mockConfigStore) Latest(ctx context.Context) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, nil
}

func (mockConfigStore) FindByID(ctx context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, nil
}

func (mockConfigStore) FindActive(ctx context.Context) (domain.ConfigVersion, error) {
	return domain.ConfigVersion{}, nil
}

func (mockConfigStore) UpdateStatus(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	return version, nil
}

type mockCredentialStore struct{}

func (mockCredentialStore) SavePassword(ctx context.Context, credential domain.PasswordCredential) error {
	return nil
}

func (mockCredentialStore) FindPassword(ctx context.Context, userID domain.UserID) (domain.PasswordCredential, error) {
	return domain.PasswordCredential{}, nil
}

func (mockCredentialStore) SavePasskey(ctx context.Context, credential domain.PasskeyCredential) error {
	return nil
}

func (mockCredentialStore) ListPasskeys(ctx context.Context, userID domain.UserID) ([]domain.PasskeyCredential, error) {
	return nil, nil
}

func (mockCredentialStore) SaveRecoveryFactor(ctx context.Context, factor domain.RecoveryFactor) error {
	return nil
}

func (mockCredentialStore) ConsumeRecoveryFactor(ctx context.Context, userID domain.UserID, codeHash string) (domain.RecoveryFactor, error) {
	return domain.RecoveryFactor{}, nil
}

type mockEventOutbox struct{}

func (mockEventOutbox) Append(ctx context.Context, event domain.OutboxEvent) error {
	return nil
}

func (mockEventOutbox) ListUnpublished(ctx context.Context, limit int) ([]domain.OutboxEvent, error) {
	return nil, nil
}

func (mockEventOutbox) MarkPublished(ctx context.Context, id domain.EventID) error {
	return nil
}

func (mockEventOutbox) RecordFailure(ctx context.Context, id domain.EventID, category contracts.ErrorCategory, component string) error {
	return nil
}

type mockEventPublisher struct{}

func (mockEventPublisher) Publish(ctx context.Context, event domain.Event) error {
	return nil
}

func (mockEventPublisher) Subscribe(eventType string, handler contracts.EventHandler) (contracts.Subscription, error) {
	return mockSubscription{}, nil
}

type mockSubscription struct{}

func (mockSubscription) Unsubscribe() {}

type mockSecretBroker struct{}

func (mockSecretBroker) Resolve(ctx context.Context, ref domain.SecretRef) (domain.Secret, error) {
	return domain.Secret{}, nil
}

func (mockSecretBroker) Rotate(ctx context.Context, ref domain.SecretRef) (domain.Secret, error) {
	return domain.Secret{}, nil
}

type mockClock struct{}

func (mockClock) Now() time.Time { return time.Time{} }

type mockIDGenerator struct{}

func (mockIDGenerator) NewID() domain.ID { return domain.ID("") }

type mockHealthProbe struct{}

func (mockHealthProbe) Check(ctx context.Context) (domain.HealthStatus, error) {
	return domain.HealthStatus{}, nil
}

type mockComponentHealthReporter struct{}

func (mockComponentHealthReporter) ReportHealth(ctx context.Context) domain.ComponentHealth {
	return domain.ComponentHealth{}
}

var (
	_ contracts.UnitOfWork              = mockUnitOfWork{}
	_ contracts.Tx                      = mockTx{}
	_ contracts.UserStore               = mockUserStore{}
	_ contracts.SessionStore            = mockSessionStore{}
	_ contracts.PermissionStore         = mockPermissionStore{}
	_ contracts.ConfigStore             = mockConfigStore{}
	_ contracts.EventOutbox             = mockEventOutbox{}
	_ contracts.EventPublisher          = mockEventPublisher{}
	_ contracts.Subscription            = mockSubscription{}
	_ contracts.SecretBroker            = mockSecretBroker{}
	_ contracts.Clock                   = mockClock{}
	_ contracts.IDGenerator             = mockIDGenerator{}
	_ contracts.HealthProbe             = mockHealthProbe{}
	_ contracts.CredentialStore         = mockCredentialStore{}
	_ contracts.ComponentHealthReporter = mockComponentHealthReporter{}
)

// TestStoreResolvesIdenticalInstanceAsNamedAccessor proves the equivalence
// MAD-001 §02 requires: "Every store — Core Platform or capability — is
// resolved the same way." For each store type, Store[T](tx) must hand back the
// exact same instance the matching named accessor would for the same tx value.
// This is the strongest guarantee the contracts package can make on its own —
// full transactional atomicity against real PostgreSQL is a later slice's job.
func TestStoreResolvesIdenticalInstanceAsNamedAccessor(t *testing.T) {
	tx := newMockTx()

	userStore, err := contracts.Store[contracts.UserStore](tx)
	if err != nil {
		t.Fatalf("Store[UserStore] returned error: %v", err)
	}
	if userStore != tx.Users() {
		t.Error("Store[UserStore] resolved a different instance than tx.Users()")
	}

	outbox, err := contracts.Store[contracts.EventOutbox](tx)
	if err != nil {
		t.Fatalf("Store[EventOutbox] returned error: %v", err)
	}
	if outbox != tx.Outbox() {
		t.Error("Store[EventOutbox] resolved a different instance than tx.Outbox()")
	}

	credentials, err := contracts.Store[contracts.CredentialStore](tx)
	if err != nil {
		t.Fatalf("Store[CredentialStore] returned error: %v", err)
	}
	if credentials != tx.Credentials() {
		t.Error("Store[CredentialStore] resolved a different instance than tx.Credentials()")
	}
}

// TestStoreFailsLoudlyForUnboundType proves the caller-facing boundary fails
// closed: a type no adapter binds to the transaction scope yields an
// Internal-category Platform error and a zero store, not a silent nil. This is
// the property that lets Store stay type-safe without a stringly-typed,
// permission-asking extension registry (MAD-001 §03, rejected option a).
func TestStoreFailsLoudlyForUnboundType(t *testing.T) {
	tx := newMockTx()

	// SecretBroker is a real contract, but it is not a transaction-scoped
	// store and is deliberately absent from Store's resolution table.
	broker, err := contracts.Store[contracts.SecretBroker](tx)
	if err == nil {
		t.Fatal("Store[SecretBroker] returned nil error for an unbound store type")
	}
	if got := contracts.CategoryOf(err); got != contracts.Internal {
		t.Errorf("Store[SecretBroker] error category = %q, want %q", got, contracts.Internal)
	}
	if broker != nil {
		t.Error("Store[SecretBroker] returned a non-nil store alongside its error")
	}
}
