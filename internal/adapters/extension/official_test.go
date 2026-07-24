// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"crypto/ed25519"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The embedded official key is a well-formed ed25519 public key. This is the
// guard against a truncated, wrong-format or accidentally-cleared key file
// reaching a release — it would otherwise fail only when a real official module
// failed to verify at a user's install.
func TestOfficialKeyIsWellFormed(t *testing.T) {
	repo, err := extension.OfficialRepository()
	if err != nil {
		t.Fatalf("the embedded official key is not usable: %v", err)
	}
	if len(repo.Key) != ed25519.PublicKeySize {
		t.Fatalf("official key is %d bytes, want %d", len(repo.Key), ed25519.PublicKeySize)
	}
	// A non-zero key: an all-zero file is a valid length but a dead key.
	allZero := true
	for _, b := range repo.Key {
		if b != 0 {
			allZero = false
			break
		}
	}
	if allZero {
		t.Fatal("the embedded official key is all zeros — the key file is empty or a placeholder")
	}
}

// The official repository is trusted by default and points at the registry's
// published index.
func TestOfficialRepositoryIsTrustedByDefault(t *testing.T) {
	repo, err := extension.OfficialRepository()
	if err != nil {
		t.Fatalf("official repository: %v", err)
	}
	if repo.Name != extension.OfficialRepositoryName {
		t.Errorf("name: got %q, want %q", repo.Name, extension.OfficialRepositoryName)
	}
	if repo.URL != extension.OfficialRepositoryURL {
		t.Errorf("url: got %q", repo.URL)
	}
	if !repo.Official {
		t.Error("the official repository is not marked Official")
	}

	reg, err := extension.DefaultRegistry()
	if err != nil {
		t.Fatalf("default registry: %v", err)
	}
	if _, ok := reg.Lookup(extension.OfficialRepositoryName); !ok {
		t.Error("the default registry does not trust the official repository")
	}
}

// NewOfficialInstaller constructs cleanly, so the install path is one call away
// once an install trigger exists.
func TestNewOfficialInstallerConstructs(t *testing.T) {
	if _, err := extension.NewOfficialInstaller(t.TempDir()); err != nil {
		t.Fatalf("building the official installer: %v", err)
	}
}
