// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build !mosaicdev

package extension_test

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// **The guard on the development override (ADR 0099), asserted in the build that
// ships.** Setting both variables — with a key file that is a perfectly valid
// ed25519 public key, so nothing here passes for an incidental reason — must
// leave this Platform trusting the compiled-in URL and the compiled-in key.
//
// This is the property a reader would otherwise have to take on faith: a release
// binary cannot be pointed at another module repository, and therefore at
// another publisher's code, by editing its environment. It holds structurally
// rather than behaviourally — devregistry_off.go contains no path from an
// environment value to a trusted key — and it would fail the moment the tag
// split were collapsed into a runtime check.
//
// Its counterpart is TestTheDevelopmentOverrideReplacesTheURLAndTheKey, which
// asserts the opposite under `-tags mosaicdev`. Neither test can run in the
// other's build, which is exactly why the gate runs both.
func TestTheEnvironmentCannotRepointAShippedBuild(t *testing.T) {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating a key: %v", err)
	}
	keyPath := filepath.Join(t.TempDir(), "dev-registry.key.pub")
	if err := os.WriteFile(keyPath, pub, 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}
	t.Setenv(extension.DevRepositoryURLEnv, "http://registry.invalid")
	t.Setenv(extension.DevRepositoryKeyEnv, keyPath)

	repo, err := extension.OfficialRepository()
	if err != nil {
		t.Fatalf("official repository: %v", err)
	}
	if repo.URL != extension.OfficialRepositoryURL {
		t.Errorf("the environment moved the repository URL to %q; a shipped build must keep %q",
			repo.URL, extension.OfficialRepositoryURL)
	}
	if bytes.Equal(repo.Key, pub) {
		t.Error("the environment replaced the trusted key; a shipped build must keep the compiled-in one")
	}
	if !repo.Official {
		t.Error("the repository stopped calling itself official; nothing overrode it")
	}
}
