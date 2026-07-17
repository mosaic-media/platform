package contracts_test

import (
	"context"
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
	return fn(ctx, mockTx{})
}

type mockTx struct{}

func (mockTx) Users() contracts.UserStore             { return mockUserStore{} }
func (mockTx) Sessions() contracts.SessionStore       { return mockSessionStore{} }
func (mockTx) Permissions() contracts.PermissionStore { return mockPermissionStore{} }
func (mockTx) Config() contracts.ConfigStore          { return mockConfigStore{} }
func (mockTx) Outbox() contracts.EventOutbox          { return mockEventOutbox{} }
func (mockTx) Credentials() contracts.CredentialStore { return mockCredentialStore{} }

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

var (
	_ contracts.UnitOfWork      = mockUnitOfWork{}
	_ contracts.Tx              = mockTx{}
	_ contracts.UserStore       = mockUserStore{}
	_ contracts.SessionStore    = mockSessionStore{}
	_ contracts.PermissionStore = mockPermissionStore{}
	_ contracts.ConfigStore     = mockConfigStore{}
	_ contracts.EventOutbox     = mockEventOutbox{}
	_ contracts.EventPublisher  = mockEventPublisher{}
	_ contracts.Subscription    = mockSubscription{}
	_ contracts.SecretBroker    = mockSecretBroker{}
	_ contracts.Clock           = mockClock{}
	_ contracts.IDGenerator     = mockIDGenerator{}
	_ contracts.HealthProbe     = mockHealthProbe{}
	_ contracts.CredentialStore = mockCredentialStore{}
)
