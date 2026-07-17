package secrets_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/secrets"
)

// TestSecretReferenceResolvesEndToEndThroughRealVault proves the full
// MEG-015 §12 exit criterion path with real components, not fakes: parse
// a secret:// reference the way a config value would carry it, resolve it
// through a Broker backed by a real encrypted LocalVault (the keychain is
// forced unavailable here, since this environment has no OS keychain
// service — see TestOSKeychainStoreDegradesGracefully), and confirm the
// value round-trips.
func TestSecretReferenceResolvesEndToEndThroughRealVault(t *testing.T) {
	vaultPath := filepath.Join(t.TempDir(), "vault.enc")
	vault := secrets.NewLocalVault(vaultPath, []byte("integration-test-recovery-key"))
	ctx := context.Background()

	ref, err := secrets.ParseRef("secret://platform/postgres/password")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}

	if err := vault.Set(ctx, ref.Name, secrets.Entry{Value: "correct horse battery staple", Version: 1}); err != nil {
		t.Fatalf("seed vault: %v", err)
	}

	broker := secrets.NewBroker(unavailableKeychain{}, vault, fakeClock{now: testNow})
	resolved, err := broker.Resolve(ctx, ref)
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	if resolved.Value != "correct horse battery staple" {
		t.Fatalf("resolved.Value = %q, want the seeded value", resolved.Value)
	}
	if secrets.FormatRef(resolved.Ref) != "secret://platform/postgres/password" {
		t.Fatalf("resolved.Ref round-trips to %q, want the original reference", secrets.FormatRef(resolved.Ref))
	}
}

// unavailableKeychain always reports Available = false, standing in for a
// host with no OS keychain service so this test deterministically exercises
// the vault fallback path, matching this sandbox's real environment
// (confirmed for real by TestOSKeychainStoreDegradesGracefully) rather than
// depending on it.
type unavailableKeychain struct{}

func (unavailableKeychain) Available(context.Context) bool { return false }
func (unavailableKeychain) Get(context.Context, string) (secrets.Entry, error) {
	panic("must not be called: the keychain is unavailable")
}
func (unavailableKeychain) Set(context.Context, string, secrets.Entry) error {
	panic("must not be called: the keychain is unavailable")
}
