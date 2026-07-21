// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package secrets

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/zalando/go-keyring"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// keychainService namespaces every secret this Broker stores in the OS
// keychain, so mosaic-platform entries never collide with another
// application's entries under the same user account.
const keychainService = "mosaic-platform"

// keychainAvailabilityProbeName is looked up (never written) to test
// whether the OS keychain backend is reachable at all, independent of
// whether any real secret has been stored yet.
const keychainAvailabilityProbeName = "mosaic-platform-availability-probe"

// OSKeychainStore is the OS keychain SecretStore the Broker prefers:
// macOS Keychain, Windows Credential Manager, or the Linux Secret Service
// (GNOME Keyring/KDE Wallet) via go-keyring — pure Go, no cgo. Entries are
// stored as a small JSON envelope so Version/RotatedAt survive the round
// trip through a backend that otherwise only stores one opaque string per
// name.
type OSKeychainStore struct{}

// NewOSKeychainStore builds an OSKeychainStore.
func NewOSKeychainStore() *OSKeychainStore { return &OSKeychainStore{} }

// Available reports whether the local OS keychain service is actually
// reachable right now, not merely whether the library is linked in. A
// lookup for a name that does not exist still proves the backend itself
// works: keyring.ErrNotFound is a normal "no such entry" answer from a
// working backend. Any other error (no Secret Service running, no
// keychain daemon unlocked, ...) means the backend could not be reached at
// all — exactly the condition that should fall back to the encrypted local
// vault.
func (k *OSKeychainStore) Available(_ context.Context) bool {
	_, err := keyring.Get(keychainService, keychainAvailabilityProbeName)
	return err == nil || errors.Is(err, keyring.ErrNotFound)
}

// Get reads and decodes the entry stored under name in the OS keychain.
func (k *OSKeychainStore) Get(_ context.Context, name string) (Entry, error) {
	raw, err := keyring.Get(keychainService, name)
	if err != nil {
		if errors.Is(err, keyring.ErrNotFound) {
			return Entry{}, contracts.NewError(contracts.NotFound, "secret not found in OS keychain")
		}
		return Entry{}, contracts.WrapError(contracts.Unavailable, "read OS keychain", err)
	}
	var entry Entry
	if err := json.Unmarshal([]byte(raw), &entry); err != nil {
		return Entry{}, contracts.WrapError(contracts.Internal, "decode OS keychain entry", err)
	}
	return entry, nil
}

// Set encodes entry and stores it under name in the OS keychain.
func (k *OSKeychainStore) Set(_ context.Context, name string, entry Entry) error {
	raw, err := json.Marshal(entry)
	if err != nil {
		return contracts.WrapError(contracts.Internal, "encode OS keychain entry", err)
	}
	if err := keyring.Set(keychainService, name, string(raw)); err != nil {
		return contracts.WrapError(contracts.Unavailable, "write OS keychain", err)
	}
	return nil
}
