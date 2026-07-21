// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package secrets

import (
	"context"
	"encoding/json"
	"sync"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/adapters/filesystem"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// LocalVault is the encrypted local vault fallback used when the OS keychain
// is unavailable: a single file holding every secret entry, encrypted with a
// key derived from a recovery key kept separate from the vault file itself.
// Losing both the vault file and the recovery key intentionally makes its
// contents unrecoverable rather than weakening protection with a recovery
// backdoor.
type LocalVault struct {
	path string
	key  [32]byte
	mu   sync.Mutex
}

// NewLocalVault builds a LocalVault backed by the file at path, encrypted
// under a key derived from recoveryKey. recoveryKey must be supplied by the
// deployment through a channel kept separate from the vault file itself (an
// environment variable, a mounted secret, ...); the vault never reads or
// stores it.
func NewLocalVault(path string, recoveryKey []byte) *LocalVault {
	return &LocalVault{path: path, key: crypto.DeriveKey(recoveryKey)}
}

// Available always reports true: the vault is the fallback of last resort
// and has no external service to be unavailable. A filesystem failure
// surfaces as an error from Get/Set, not as unavailability up front.
func (v *LocalVault) Available(_ context.Context) bool { return true }

// Get returns the entry stored under name, or NotFound if none exists.
func (v *LocalVault) Get(_ context.Context, name string) (Entry, error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.load()
	if err != nil {
		return Entry{}, err
	}
	entry, ok := entries[name]
	if !ok {
		return Entry{}, contracts.NewError(contracts.NotFound, "secret not found in vault")
	}
	return entry, nil
}

// Set stores entry under name, replacing any existing value.
func (v *LocalVault) Set(_ context.Context, name string, entry Entry) error {
	v.mu.Lock()
	defer v.mu.Unlock()

	entries, err := v.load()
	if err != nil {
		return err
	}
	entries[name] = entry
	return v.save(entries)
}

// load decrypts and decodes the vault file, or returns an empty entry set
// if it does not exist yet (a fresh install's first secret write).
func (v *LocalVault) load() (map[string]Entry, error) {
	if !filesystem.Exists(v.path) {
		return map[string]Entry{}, nil
	}
	ciphertext, err := filesystem.ReadFile(v.path)
	if err != nil {
		return nil, contracts.WrapError(contracts.Internal, "read vault file", err)
	}
	plaintext, err := crypto.Decrypt(v.key, ciphertext)
	if err != nil {
		// A wrong recovery key produces an authentication failure here, not
		// a decodable-but-wrong plaintext (AES-GCM), so this also covers
		// "recovery key does not match this vault".
		return nil, contracts.WrapError(contracts.Internal, "decrypt vault (wrong recovery key or corrupt file)", err)
	}
	var entries map[string]Entry
	if err := json.Unmarshal(plaintext, &entries); err != nil {
		return nil, contracts.WrapError(contracts.Internal, "decode vault contents", err)
	}
	return entries, nil
}

func (v *LocalVault) save(entries map[string]Entry) error {
	plaintext, err := json.Marshal(entries)
	if err != nil {
		return contracts.WrapError(contracts.Internal, "encode vault contents", err)
	}
	ciphertext, err := crypto.Encrypt(v.key, plaintext)
	if err != nil {
		return contracts.WrapError(contracts.Internal, "encrypt vault", err)
	}
	if err := filesystem.WriteFileAtomic(v.path, ciphertext); err != nil {
		return contracts.WrapError(contracts.Internal, "write vault file", err)
	}
	return nil
}
