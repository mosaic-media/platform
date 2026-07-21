// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package secrets_test

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/secrets"
)

func TestLocalVaultAlwaysAvailable(t *testing.T) {
	vault := secrets.NewLocalVault(filepath.Join(t.TempDir(), "vault.enc"), []byte("recovery-key"))
	if !vault.Available(context.Background()) {
		t.Fatal("expected the local vault to always report Available = true")
	}
}

func TestLocalVaultGetOnEmptyVaultIsNotFound(t *testing.T) {
	vault := secrets.NewLocalVault(filepath.Join(t.TempDir(), "vault.enc"), []byte("recovery-key"))
	_, err := vault.Get(context.Background(), "platform/postgres/password")
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.NotFound)
	}
}

func TestLocalVaultSetThenGetRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	vault := secrets.NewLocalVault(path, []byte("recovery-key"))
	ctx := context.Background()

	rotatedAt := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	want := secrets.Entry{Value: "correct horse battery staple", Version: 3, RotatedAt: rotatedAt}
	if err := vault.Set(ctx, "platform/postgres/password", want); err != nil {
		t.Fatalf("Set: %v", err)
	}

	got, err := vault.Get(ctx, "platform/postgres/password")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.Value != want.Value || got.Version != want.Version || !got.RotatedAt.Equal(want.RotatedAt) {
		t.Fatalf("Get() = %+v, want %+v", got, want)
	}
}

func TestLocalVaultPersistsAcrossInstancesUnderTheSameRecoveryKey(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	ctx := context.Background()

	first := secrets.NewLocalVault(path, []byte("shared-recovery-key"))
	if err := first.Set(ctx, "platform/example/token", secrets.Entry{Value: "token-value", Version: 1}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// A second, independent LocalVault instance over the same file and
	// recovery key must read what the first wrote — the vault survives a
	// process restart, unlike an in-memory-only store.
	second := secrets.NewLocalVault(path, []byte("shared-recovery-key"))
	got, err := second.Get(ctx, "platform/example/token")
	if err != nil {
		t.Fatalf("Get from second instance: %v", err)
	}
	if got.Value != "token-value" {
		t.Fatalf("got.Value = %q, want %q", got.Value, "token-value")
	}
}

func TestLocalVaultWrongRecoveryKeyFailsToDecrypt(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	ctx := context.Background()

	writer := secrets.NewLocalVault(path, []byte("correct-recovery-key"))
	if err := writer.Set(ctx, "platform/example/token", secrets.Entry{Value: "token-value"}); err != nil {
		t.Fatalf("Set: %v", err)
	}

	reader := secrets.NewLocalVault(path, []byte("wrong-recovery-key"))
	_, err := reader.Get(ctx, "platform/example/token")
	if err == nil {
		t.Fatal("expected an error reading the vault with the wrong recovery key")
	}
	if got := contracts.CategoryOf(err); got != contracts.Internal {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Internal)
	}
}

func TestLocalVaultSetPreservesOtherEntries(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")
	vault := secrets.NewLocalVault(path, []byte("recovery-key"))
	ctx := context.Background()

	if err := vault.Set(ctx, "platform/postgres/password", secrets.Entry{Value: "pg-pass"}); err != nil {
		t.Fatalf("Set(postgres): %v", err)
	}
	if err := vault.Set(ctx, "platform/example/token", secrets.Entry{Value: "example-token"}); err != nil {
		t.Fatalf("Set(example): %v", err)
	}

	pg, err := vault.Get(ctx, "platform/postgres/password")
	if err != nil || pg.Value != "pg-pass" {
		t.Fatalf("Get(postgres) = %+v, %v", pg, err)
	}
	example, err := vault.Get(ctx, "platform/example/token")
	if err != nil || example.Value != "example-token" {
		t.Fatalf("Get(example) = %+v, %v", example, err)
	}
}
