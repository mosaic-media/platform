// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build mosaicdev

// The development-override tests (platform#55). They run only under -tags
// mosaicdev, which the test gate runs as a second pass — the override exists in
// exactly one build configuration, so it is tested in that one rather than left
// as the untested half of a tag split.
//
// Their counterpart is TestTheEnvironmentCannotRepointAShippedBuild in
// official_test.go, which asserts the opposite in the default build: one file
// proves the mechanism works, the other proves it is absent.

package extension_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// devRegistry stands up a signed index over HTTP on loopback — the shape the dev
// stack's local registry has, minus the compose network. It returns the base URL
// and the path of the public key file the Platform is pointed at.
//
// signWith lets a test sign the index with a key other than the one it hands the
// Platform, which is how "verification is still on" is asserted rather than
// claimed.
func devRegistry(t *testing.T, signWith func(indexPub ed25519.PublicKey, indexPriv ed25519.PrivateKey) ed25519.PrivateKey) (string, string) {
	t.Helper()

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating the development key: %v", err)
	}
	signer := priv
	if signWith != nil {
		signer = signWith(pub, priv)
	}

	index, err := json.Marshal(map[string]any{
		"schema": extension.IndexSchema,
		"modules": []map[string]any{{
			"manifest": map[string]any{
				"schema":      extension.ManifestSchema,
				"id":          "stremio",
				"version":     "local-v0.28.0-dirty",
				"name":        "Stremio addon source",
				"description": "built from the sibling checkout",
				"sdk_major":   0,
				"provides":    []string{string(v1.RoleSearch)},
				"binaries": []map[string]string{
					{"os": "linux", "arch": "amd64", "digest": "sha256:00", "url": "/stremio/module"},
				},
			},
		}},
	})
	if err != nil {
		t.Fatalf("marshalling the index: %v", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write(index) })
	mux.HandleFunc("/index.json.sig", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write(ed25519.Sign(signer, index))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	keyPath := filepath.Join(t.TempDir(), "dev-registry.key.pub")
	if err := os.WriteFile(keyPath, pub, 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}
	return srv.URL, keyPath
}

// The override replaces both halves and the repository stops calling itself
// official — which is what the boot warning keys on.
func TestTheDevelopmentOverrideReplacesTheURLAndTheKey(t *testing.T) {
	url, keyPath := devRegistry(t, nil)
	t.Setenv(extension.DevRepositoryURLEnv, url)
	t.Setenv(extension.DevRepositoryKeyEnv, keyPath)

	repo, err := extension.OfficialRepository()
	if err != nil {
		t.Fatalf("official repository: %v", err)
	}
	if repo.URL != url {
		t.Errorf("url: got %q, want %q", repo.URL, url)
	}
	if repo.Name != extension.OfficialRepositoryName {
		t.Errorf("name: got %q, want %q — the override moves where the official repository points, not what it is called",
			repo.Name, extension.OfficialRepositoryName)
	}
	if repo.Official {
		t.Error("an overridden repository still claims to be Mosaic's official one")
	}
	if repo.KeyFingerprint() == "" {
		t.Error("no key fingerprint to log at boot")
	}
}

// The override end to end: a catalogue is fetched from a local HTTP registry on
// loopback and verified against the development key — which takes the URL
// override, the key override and the relaxed dialer all being in effect, since
// netguard refuses loopback and the compiled-in key did not sign this index.
func TestTheDevelopmentRepositoryIsFetchedAndVerified(t *testing.T) {
	url, keyPath := devRegistry(t, nil)
	t.Setenv(extension.DevRepositoryURLEnv, url)
	t.Setenv(extension.DevRepositoryKeyEnv, keyPath)

	installer, err := extension.NewOfficialInstaller(t.TempDir())
	if err != nil {
		t.Fatalf("building the installer: %v", err)
	}
	manifests, err := installer.Catalogue(context.Background(), extension.OfficialRepositoryName)
	if err != nil {
		t.Fatalf("cataloguing the development repository: %v", err)
	}
	if len(manifests) != 1 || manifests[0].ID != "stremio" {
		t.Fatalf("catalogue: got %+v, want one entry for stremio", manifests)
	}
	if manifests[0].Description != "built from the sibling checkout" {
		t.Errorf("description: got %q — the module's own text should reach the catalogue unaltered", manifests[0].Description)
	}
}

// Verification is not bypassed, it is re-keyed. An index signed by a key the
// Platform was not given is refused exactly as an unsigned official index would
// be. Without this the override would be a hole rather than a loop: the local
// path would prove nothing about the real one.
func TestADevelopmentIndexSignedByTheWrongKeyIsRefused(t *testing.T) {
	url, keyPath := devRegistry(t, func(_ ed25519.PublicKey, _ ed25519.PrivateKey) ed25519.PrivateKey {
		_, other, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatalf("generating the impostor key: %v", err)
		}
		return other
	})
	t.Setenv(extension.DevRepositoryURLEnv, url)
	t.Setenv(extension.DevRepositoryKeyEnv, keyPath)

	installer, err := extension.NewOfficialInstaller(t.TempDir())
	if err != nil {
		t.Fatalf("building the installer: %v", err)
	}
	if _, err := installer.Catalogue(context.Background(), extension.OfficialRepositoryName); err == nil {
		t.Fatal("an index signed by an untrusted key was accepted")
	} else if !strings.Contains(err.Error(), "does not verify") {
		t.Fatalf("refused for the wrong reason: %v", err)
	}
}

// Half an override is a boot failure. The alternative — falling back to the
// official repository — answers "why is my local module not in the list?" three
// layers away from the typo that caused it.
func TestAHalfConfiguredOverrideFailsTheBoot(t *testing.T) {
	url, keyPath := devRegistry(t, nil)

	for _, tc := range []struct {
		name, url, key, wants string
	}{
		{"url without key", url, "", extension.DevRepositoryKeyEnv},
		{"key without url", "", keyPath, extension.DevRepositoryURLEnv},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(extension.DevRepositoryURLEnv, tc.url)
			t.Setenv(extension.DevRepositoryKeyEnv, tc.key)
			_, err := extension.OfficialRepository()
			if err == nil {
				t.Fatal("a half-configured override was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the error does not name %s: %v", tc.wants, err)
			}
		})
	}
}

// A key file that is not an ed25519 public key fails at boot, with its size in
// the message — the same class of check the embedded official key gets, and the
// same reason: a wrong key here surfaces otherwise as "nothing verifies" at the
// first install.
func TestAMalformedDevelopmentKeyFailsTheBoot(t *testing.T) {
	dir := t.TempDir()
	short := filepath.Join(dir, "short.pub")
	if err := os.WriteFile(short, []byte("not a key"), 0o600); err != nil {
		t.Fatalf("writing the key: %v", err)
	}

	for _, tc := range []struct{ name, key, wants string }{
		{"wrong size", short, "not an ed25519 public key"},
		{"absent", filepath.Join(dir, "nothing-here.pub"), "reading the development repository key"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(extension.DevRepositoryURLEnv, "http://registry.invalid")
			t.Setenv(extension.DevRepositoryKeyEnv, tc.key)
			_, err := extension.OfficialRepository()
			if err == nil {
				t.Fatal("a malformed key was accepted")
			}
			if !strings.Contains(err.Error(), tc.wants) {
				t.Fatalf("the error does not say why: %v", err)
			}
		})
	}
}

// With nothing set, a development build is a normal one: the compiled-in
// repository, trusted by default, fetched through the dial guard. The tag makes
// the override possible, not active.
func TestADevelopmentBuildWithNoOverrideIsUnchanged(t *testing.T) {
	t.Setenv(extension.DevRepositoryURLEnv, "")
	t.Setenv(extension.DevRepositoryKeyEnv, "")

	repo, err := extension.OfficialRepository()
	if err != nil {
		t.Fatalf("official repository: %v", err)
	}
	if repo.URL != extension.OfficialRepositoryURL {
		t.Errorf("url: got %q, want the compiled-in %q", repo.URL, extension.OfficialRepositoryURL)
	}
	if !repo.Official {
		t.Error("the repository is not Official with no override in effect")
	}
}
