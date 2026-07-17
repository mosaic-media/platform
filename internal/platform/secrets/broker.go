package secrets

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"sync"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// Broker is the Platform's contracts.SecretBroker implementation
// (MEG-015 §08). It prefers the OS keychain and falls back to the
// encrypted local vault when the keychain is unavailable; which backend a
// given deployment actually uses is resolved once (not re-probed per
// call), matching "local deployments use the operating-system keychain
// where available" as a deployment-level characteristic, not a per-secret
// choice (MEG-005 §19).
type Broker struct {
	keychain SecretStore
	vault    SecretStore
	clock    contracts.Clock

	mu     sync.Mutex
	active SecretStore
}

// NewBroker builds a Broker backed by keychain (tried first) and vault
// (used if keychain is unavailable).
func NewBroker(keychain, vault SecretStore, clock contracts.Clock) *Broker {
	return &Broker{keychain: keychain, vault: vault, clock: clock}
}

// backend resolves, and caches, which SecretStore this Broker uses.
func (b *Broker) backend(ctx context.Context) SecretStore {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.active != nil {
		return b.active
	}
	if b.keychain != nil && b.keychain.Available(ctx) {
		b.active = b.keychain
	} else {
		b.active = b.vault
	}
	return b.active
}

// Resolve looks up ref's current value through whichever backend this
// Broker is using.
func (b *Broker) Resolve(ctx context.Context, ref domain.SecretRef) (domain.Secret, error) {
	if ref.Name == "" {
		return domain.Secret{}, contracts.NewError(contracts.InvalidArgument, "secret name is required")
	}
	entry, err := b.backend(ctx).Get(ctx, ref.Name)
	if err != nil {
		return domain.Secret{}, err
	}
	return domain.Secret{Ref: ref, Value: entry.Value, Version: entry.Version, RotatedAt: entry.RotatedAt}, nil
}

// Rotate generates a new, cryptographically random value for ref, persists
// it with an incremented version, and returns it. Secret versions rotate
// independently of ordinary configuration (MEG-005 §19): the config value
// (a secret:// reference) never has to change, only what it resolves to.
func (b *Broker) Rotate(ctx context.Context, ref domain.SecretRef) (domain.Secret, error) {
	if ref.Name == "" {
		return domain.Secret{}, contracts.NewError(contracts.InvalidArgument, "secret name is required")
	}

	store := b.backend(ctx)
	nextVersion := 1
	existing, err := store.Get(ctx, ref.Name)
	switch {
	case err == nil:
		nextVersion = existing.Version + 1
	case contracts.CategoryOf(err) == contracts.NotFound:
		// First rotation for a secret that has never been set.
	default:
		return domain.Secret{}, err
	}

	value, err := randomSecretValue()
	if err != nil {
		return domain.Secret{}, contracts.WrapError(contracts.Internal, "generate secret value", err)
	}

	entry := Entry{Value: value, Version: nextVersion, RotatedAt: b.clock.Now()}
	if err := store.Set(ctx, ref.Name, entry); err != nil {
		return domain.Secret{}, err
	}
	return domain.Secret{Ref: ref, Value: entry.Value, Version: entry.Version, RotatedAt: entry.RotatedAt}, nil
}

// randomSecretValue returns a base64url-encoded, cryptographically random
// 32-byte value suitable as a rotated credential.
func randomSecretValue() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw), nil
}
