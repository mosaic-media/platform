package secrets

import (
	"context"
	"time"
)

// Entry is a stored secret value plus the version/rotation bookkeeping a
// SecretStore backend persists alongside it (MEG-005 §19: "secret metadata
// — availability, version, expiry and last rotation").
type Entry struct {
	Value     string    `json:"value"`
	Version   int       `json:"version"`
	RotatedAt time.Time `json:"rotated_at"`
}

// SecretStore is a secret storage backend behind a Broker: either the OS
// keychain or the encrypted local vault fallback (MEG-015 §08).
type SecretStore interface {
	// Available reports whether this store can be used right now. It does
	// not report whether any particular secret exists — a working store
	// with no matching entry is still Available; Get returns NotFound for
	// that case instead.
	Available(ctx context.Context) bool
	Get(ctx context.Context, name string) (Entry, error)
	Set(ctx context.Context, name string, entry Entry) error
}
