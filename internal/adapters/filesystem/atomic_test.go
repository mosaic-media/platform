// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package filesystem_test

import (
	"bytes"
	"path/filepath"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/filesystem"
)

func TestWriteFileAtomicThenReadFileRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "vault.enc")

	if filesystem.Exists(path) {
		t.Fatal("expected Exists = false before any write")
	}

	want := []byte("ciphertext-bytes")
	if err := filesystem.WriteFileAtomic(path, want); err != nil {
		t.Fatalf("WriteFileAtomic: %v", err)
	}
	if !filesystem.Exists(path) {
		t.Fatal("expected Exists = true after WriteFileAtomic")
	}

	got, err := filesystem.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadFile() = %q, want %q", got, want)
	}
}

func TestWriteFileAtomicOverwritesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vault.enc")

	if err := filesystem.WriteFileAtomic(path, []byte("first")); err != nil {
		t.Fatalf("WriteFileAtomic(first): %v", err)
	}
	if err := filesystem.WriteFileAtomic(path, []byte("second")); err != nil {
		t.Fatalf("WriteFileAtomic(second): %v", err)
	}

	got, err := filesystem.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "second" {
		t.Fatalf("ReadFile() = %q, want %q", got, "second")
	}
}

func TestExistsFalseForMissingFile(t *testing.T) {
	if filesystem.Exists(filepath.Join(t.TempDir(), "does-not-exist")) {
		t.Fatal("expected Exists = false for a missing file")
	}
}
