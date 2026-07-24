// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/json"
	"os"
	"runtime"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// fakeRepo is a repository in memory: a signed index and the artefacts it names,
// served by a Fetcher. It is the whole of a repository the install flow needs,
// without a live HTTPS server — the flow's job is verify-then-store, and a fake
// serves that faithfully while staying hermetic.
type fakeRepo struct {
	files map[string][]byte // url -> bytes
}

func (r *fakeRepo) Fetch(_ context.Context, url string) ([]byte, error) {
	data, ok := r.files[url]
	if !ok {
		return nil, contracts.NewError(contracts.NotFound, "fake repo: no "+url)
	}
	return data, nil
}

// newFakeRepo builds a signed index offering the probe, and returns the repo, a
// fetcher serving it, and the registry it is trusted in. mutate lets a test
// corrupt one artefact after signing to exercise a failure.
func newFakeRepo(t *testing.T, mutate func(files map[string][]byte, priv ed25519.PrivateKey)) (extension.Repository, *fakeRepo, *extension.Registry) {
	t.Helper()

	probe := buildProbe(t)
	binaryData, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("reading probe: %v", err)
	}
	digest := digestOf(t, probe)

	const base = "https://repo.invalid/mosaic"
	binURL := base + "/modules/extprobe/module"

	index := map[string]any{
		"schema": extension.IndexSchema,
		"modules": []map[string]any{{
			"manifest": map[string]any{
				"schema":    extension.ManifestSchema,
				"id":        "extprobe",
				"version":   "v0.1.0",
				"name":      "Extension Probe",
				"sdk_major": 0,
				"provides":  []string{string(v1.RoleSearch)},
				"binaries": []map[string]string{
					{"os": runtime.GOOS, "arch": runtime.GOARCH, "digest": digest},
				},
			},
			"binary_urls": map[string]string{
				runtime.GOOS + "/" + runtime.GOARCH: binURL,
			},
		}},
	}
	indexBytes, err := json.Marshal(index)
	if err != nil {
		t.Fatalf("marshalling index: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("key: %v", err)
	}

	files := map[string][]byte{
		base + "/index.json":     indexBytes,
		base + "/index.json.sig": ed25519.Sign(priv, indexBytes),
		binURL:                   binaryData,
	}
	if mutate != nil {
		mutate(files, priv)
	}

	repo := extension.Repository{Name: "mosaic-official", URL: base, Key: pub, Official: true}
	reg := extension.NewRegistry()
	if err := reg.Add(repo); err != nil {
		t.Fatalf("adding repo: %v", err)
	}
	return repo, &fakeRepo{files: files}, reg
}

// The whole flow: fetch a signed index, verify it, download the binary, check
// its digest against the signed manifest, and produce a runnable module with its
// provenance kept.
func TestInstallVerifiesAndKeepsProvenance(t *testing.T) {
	_, fetch, reg := newFakeRepo(t, nil)
	inst := extension.NewInstaller(reg, fetch, t.TempDir())

	got, err := inst.Install(context.Background(), "mosaic-official", "extprobe")
	if err != nil {
		t.Fatalf("install: %v", err)
	}
	if got.Repository != "mosaic-official" {
		t.Errorf("provenance: got %q, want mosaic-official", got.Repository)
	}
	if got.ModuleID != "extprobe" || got.Version != "v0.1.0" {
		t.Errorf("installed: got %+v", got)
	}

	// It is genuinely installed and runnable — the installed Config launches, and
	// the handshake confirms the running binary is the one that was signed for.
	m, err := extension.Launch(got.Config)
	if err != nil {
		t.Fatalf("launching the installed module: %v", err)
	}
	t.Cleanup(m.Close)
	if m.Capability.Manifest().ID != "extprobe" {
		t.Errorf("running id: got %q", m.Capability.Manifest().ID)
	}
}

// A tampered index — the repository's own signature no longer covers it — is
// refused. This is the "came from the repository unaltered" check.
func TestInstallRejectsATamperedIndex(t *testing.T) {
	_, fetch, reg := newFakeRepo(t, func(files map[string][]byte, _ ed25519.PrivateKey) {
		idx := files["https://repo.invalid/mosaic/index.json"]
		idx[len(idx)/2] ^= 0xff // change a byte after signing
	})
	inst := extension.NewInstaller(reg, fetch, t.TempDir())

	_, err := inst.Install(context.Background(), "mosaic-official", "extprobe")
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Errorf("category: got %q, want permission_denied (%v)", got, err)
	}
}

// An index signed by a key the repository is not trusted for is refused, even
// though it is validly signed by *some* key.
func TestInstallRejectsAnIndexFromTheWrongKey(t *testing.T) {
	_, fetch, reg := newFakeRepo(t, func(files map[string][]byte, _ ed25519.PrivateKey) {
		_, otherPriv, _ := ed25519.GenerateKey(rand.Reader)
		idx := files["https://repo.invalid/mosaic/index.json"]
		files["https://repo.invalid/mosaic/index.json.sig"] = ed25519.Sign(otherPriv, idx)
	})
	inst := extension.NewInstaller(reg, fetch, t.TempDir())

	_, err := inst.Install(context.Background(), "mosaic-official", "extprobe")
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Errorf("category: got %q, want permission_denied (%v)", got, err)
	}
}

// The index is authentic but the binary served is not the one it vouches for — a
// tampered mirror. The digest check catches it, and the bad binary is not left
// on disk.
func TestInstallRejectsATamperedBinary(t *testing.T) {
	_, fetch, reg := newFakeRepo(t, func(files map[string][]byte, _ ed25519.PrivateKey) {
		files["https://repo.invalid/mosaic/modules/extprobe/module"] = []byte("not the real binary")
	})
	dir := t.TempDir()
	inst := extension.NewInstaller(reg, fetch, dir)

	_, err := inst.Install(context.Background(), "mosaic-official", "extprobe")
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Errorf("category: got %q, want permission_denied (%v)", got, err)
	}
	// The rejected binary must not survive on disk.
	if entries, _ := os.ReadDir(dir + "/extprobe"); len(entries) != 0 {
		t.Errorf("a rejected binary was left on disk: %v", entries)
	}
}

// An install from a repository that was never added is refused — you cannot
// install from a source the user did not consent to.
func TestInstallRejectsAnUnknownRepository(t *testing.T) {
	_, fetch, reg := newFakeRepo(t, nil)
	inst := extension.NewInstaller(reg, fetch, t.TempDir())

	_, err := inst.Install(context.Background(), "some-repo-nobody-added", "extprobe")
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Errorf("category: got %q, want not_found (%v)", got, err)
	}
}

// A module the repository does not offer is a not-found, not a vaguer failure.
func TestInstallRejectsAModuleNotInTheIndex(t *testing.T) {
	_, fetch, reg := newFakeRepo(t, nil)
	inst := extension.NewInstaller(reg, fetch, t.TempDir())

	_, err := inst.Install(context.Background(), "mosaic-official", "no-such-module")
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Errorf("category: got %q, want not_found (%v)", got, err)
	}
}

// A registry rejects a repository with no valid key, so a misconfiguration is
// caught when the repository is added rather than at first install.
func TestRegistryRejectsAKeylessRepository(t *testing.T) {
	reg := extension.NewRegistry()
	if err := reg.Add(extension.Repository{Name: "bad", URL: "https://x.invalid"}); err == nil {
		t.Fatal("a repository with no key was accepted")
	}
}
