package secrets_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/secrets"
)

type fakeClock struct{ now time.Time }

func (c fakeClock) Now() time.Time { return c.now }

// fakeStore is an in-memory secrets.SecretStore stand-in for both the
// keychain and vault positions, so Broker's backend-selection logic can be
// tested deterministically without any real OS or filesystem dependency.
type fakeStore struct {
	name      string
	available bool
	entries   map[string]secrets.Entry
	calls     []string
}

func newFakeStore(name string, available bool) *fakeStore {
	return &fakeStore{name: name, available: available, entries: map[string]secrets.Entry{}}
}

func (s *fakeStore) Available(context.Context) bool {
	s.calls = append(s.calls, s.name+".available")
	return s.available
}

func (s *fakeStore) Get(_ context.Context, name string) (secrets.Entry, error) {
	s.calls = append(s.calls, s.name+".get:"+name)
	entry, ok := s.entries[name]
	if !ok {
		return secrets.Entry{}, contracts.NewError(contracts.NotFound, "secret not found")
	}
	return entry, nil
}

func (s *fakeStore) Set(_ context.Context, name string, entry secrets.Entry) error {
	s.calls = append(s.calls, s.name+".set:"+name)
	s.entries[name] = entry
	return nil
}

var testNow = time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)

func TestBrokerResolvesThroughKeychainWhenAvailable(t *testing.T) {
	keychain := newFakeStore("keychain", true)
	keychain.entries["platform/postgres/password"] = secrets.Entry{Value: "pg-pass", Version: 1, RotatedAt: testNow}
	vault := newFakeStore("vault", true)
	vault.entries["platform/postgres/password"] = secrets.Entry{Value: "should-never-be-read", Version: 99}

	broker := secrets.NewBroker(keychain, vault, fakeClock{now: testNow})
	ref := domain.SecretRef{Name: "platform/postgres/password"}

	got, err := broker.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Value != "pg-pass" || got.Version != 1 {
		t.Fatalf("Resolve() = %+v, want the keychain entry", got)
	}
	for _, call := range vault.calls {
		t.Fatalf("vault must not be touched when the keychain is available, but got call %q", call)
	}
}

func TestBrokerFallsBackToVaultWhenKeychainUnavailable(t *testing.T) {
	keychain := newFakeStore("keychain", false)
	vault := newFakeStore("vault", true)
	vault.entries["platform/postgres/password"] = secrets.Entry{Value: "vault-pass", Version: 2, RotatedAt: testNow}

	broker := secrets.NewBroker(keychain, vault, fakeClock{now: testNow})
	ref := domain.SecretRef{Name: "platform/postgres/password"}

	got, err := broker.Resolve(context.Background(), ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if got.Value != "vault-pass" || got.Version != 2 {
		t.Fatalf("Resolve() = %+v, want the vault entry", got)
	}
	for _, call := range keychain.calls {
		if call == "keychain.get:platform/postgres/password" {
			t.Fatal("keychain.Get must not be called once the keychain is known unavailable")
		}
	}
}

func TestBrokerCachesBackendSelectionAcrossCalls(t *testing.T) {
	keychain := newFakeStore("keychain", true)
	vault := newFakeStore("vault", true)
	broker := secrets.NewBroker(keychain, vault, fakeClock{now: testNow})
	ctx := context.Background()
	ref := domain.SecretRef{Name: "platform/example/token"}

	if _, err := broker.Resolve(ctx, ref); err == nil {
		t.Fatal("expected NotFound for an unset secret")
	}
	if _, err := broker.Resolve(ctx, ref); err == nil {
		t.Fatal("expected NotFound for an unset secret")
	}

	availabilityChecks := 0
	for _, call := range keychain.calls {
		if call == "keychain.available" {
			availabilityChecks++
		}
	}
	if availabilityChecks != 1 {
		t.Fatalf("keychain.Available was called %d times, want exactly 1 (the choice must be cached)", availabilityChecks)
	}
}

func TestBrokerResolveUnknownSecretIsNotFound(t *testing.T) {
	broker := secrets.NewBroker(newFakeStore("keychain", true), newFakeStore("vault", true), fakeClock{now: testNow})
	_, err := broker.Resolve(context.Background(), domain.SecretRef{Name: "platform/does/not/exist"})
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.NotFound)
	}
}

func TestBrokerResolveRejectsEmptyRef(t *testing.T) {
	broker := secrets.NewBroker(newFakeStore("keychain", true), newFakeStore("vault", true), fakeClock{now: testNow})
	_, err := broker.Resolve(context.Background(), domain.SecretRef{})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.InvalidArgument)
	}
}

func TestBrokerRotateGeneratesNewValueAndIncrementsVersion(t *testing.T) {
	vault := newFakeStore("vault", true)
	broker := secrets.NewBroker(newFakeStore("keychain", false), vault, fakeClock{now: testNow})
	ctx := context.Background()
	ref := domain.SecretRef{Name: "platform/postgres/password"}

	first, err := broker.Rotate(ctx, ref)
	if err != nil {
		t.Fatalf("Rotate(first): %v", err)
	}
	if first.Version != 1 || first.Value == "" {
		t.Fatalf("first rotation = %+v, want version 1 and a non-empty value", first)
	}
	if !first.RotatedAt.Equal(testNow) {
		t.Fatalf("first.RotatedAt = %v, want %v", first.RotatedAt, testNow)
	}

	second, err := broker.Rotate(ctx, ref)
	if err != nil {
		t.Fatalf("Rotate(second): %v", err)
	}
	if second.Version != 2 {
		t.Fatalf("second.Version = %d, want 2", second.Version)
	}
	if second.Value == first.Value {
		t.Fatal("expected a rotation to generate a different value")
	}

	resolved, err := broker.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve after rotate: %v", err)
	}
	if resolved.Value != second.Value || resolved.Version != 2 {
		t.Fatalf("Resolve() after rotate = %+v, want the latest rotation persisted", resolved)
	}
}

func TestBrokerRotateRejectsEmptyRef(t *testing.T) {
	broker := secrets.NewBroker(newFakeStore("keychain", true), newFakeStore("vault", true), fakeClock{now: testNow})
	_, err := broker.Rotate(context.Background(), domain.SecretRef{})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.InvalidArgument)
	}
}

var _ contracts.SecretBroker = (*secrets.Broker)(nil)
