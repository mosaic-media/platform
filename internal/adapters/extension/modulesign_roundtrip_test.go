// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"crypto/ed25519"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The publisher tool and the Platform verifier meet here: modulesign signs, the
// Platform's Verify accepts. It is the one test of the tool, and it is the claim
// that matters — a signature the tool produces is one the Platform trusts, and
// the digest the tool prints is the one the Platform hashes to. Those two agree
// only if both sides use the same functions, which is why the digest lives in a
// shared exported helper rather than a format written down twice.
func TestModulesignOutputVerifies(t *testing.T) {
	dir := t.TempDir()
	tool := buildModulesign(t, dir)
	probe := buildProbe(t)

	keyPath := filepath.Join(dir, "key")
	run(t, tool, "genkey", "-out", keyPath)

	// The public key the tool wrote is what the Platform trusts.
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("reading public key: %v", err)
	}
	keyring := extension.NewKeyring()
	if err := keyring.Trust("publisher", ed25519.PublicKey(pub)); err != nil {
		t.Fatalf("trusting the tool's key: %v", err)
	}

	// The digest the tool computes, in the format the Platform verifies against.
	digest := strings.TrimSpace(run(t, tool, "digest", probe))
	if !strings.HasPrefix(digest, "sha256:") {
		t.Fatalf("digest is not in the expected format: %q", digest)
	}

	// A manifest a publisher would author, pointing at that digest for this
	// platform.
	manifest := map[string]any{
		"schema":    extension.ManifestSchema,
		"id":        "extprobe",
		"version":   "v0.1.0",
		"name":      "Extension Probe",
		"sdk_major": 0,
		"provides":  []string{string(v1.RoleSearch)},
		"binaries": []map[string]string{
			{"os": runtime.GOOS, "arch": runtime.GOARCH, "digest": digest},
		},
	}
	manifestPath := filepath.Join(dir, "manifest.json")
	data, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("marshalling manifest: %v", err)
	}
	if err := os.WriteFile(manifestPath, data, 0o600); err != nil {
		t.Fatalf("writing manifest: %v", err)
	}

	// The tool signs it.
	run(t, tool, "sign", "-key", keyPath, manifestPath)

	// The Platform verifies the tool's output, end to end.
	v, err := extension.VerifyFiles(manifestPath, manifestPath+".sig", probe, keyring)
	if err != nil {
		t.Fatalf("the Platform rejected the tool's signed module: %v", err)
	}
	if v.SignedBy != "publisher" {
		t.Errorf("provenance: got %q, want publisher", v.SignedBy)
	}
	if v.Config.DeclaredManifest.ID != "extprobe" {
		t.Errorf("declared id: got %q", v.Config.DeclaredManifest.ID)
	}
}

// The producer story end to end: the tool builds a repository index from a
// manifest, signs it, and the Platform installs from it. This is what "host a
// repository — on GitHub or anywhere" reduces to: signed static files the
// Platform verifies, the host untrusted throughout.
func TestBuildIndexProducesAnInstallableRepository(t *testing.T) {
	dir := t.TempDir()
	tool := buildModulesign(t, dir)
	probe := buildProbe(t)

	keyPath := filepath.Join(dir, "key")
	run(t, tool, "genkey", "-out", keyPath)
	pub, err := os.ReadFile(keyPath + ".pub")
	if err != nil {
		t.Fatalf("reading public key: %v", err)
	}

	// A publisher's manifest, digest and URL computed by the tool. The URL
	// points at a GitHub-releases-style download location the module chose — the
	// module owns its URLs, so they live in the manifest, not a registry
	// template.
	digest := strings.TrimSpace(run(t, tool, "digest", probe))
	binURL := "https://github.invalid/registry/releases/download/extprobe-v1.0.0/extprobe-" + runtime.GOOS + "-" + runtime.GOARCH
	manifest := map[string]any{
		"schema": extension.ManifestSchema, "id": "extprobe", "version": "v1.0.0",
		"name": "Extension Probe", "sdk_major": 0,
		"provides": []string{string(v1.RoleSearch)},
		"binaries": []map[string]string{
			{"os": runtime.GOOS, "arch": runtime.GOARCH, "digest": digest, "url": binURL},
		},
	}
	manifestPath := filepath.Join(dir, "extprobe.json")
	writeJSONFile(t, manifestPath, manifest)

	// Build the index from the manifest (which already carries the URLs), then
	// sign it.
	indexPath := filepath.Join(dir, "index.json")
	run(t, tool, "build-index", "-out", indexPath, manifestPath)
	run(t, tool, "sign-index", "-key", keyPath, indexPath)

	// Serve the built index and the binary at the URL the index names, and
	// install from it.
	indexBytes, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("reading index: %v", err)
	}
	sigBytes, err := os.ReadFile(indexPath + ".sig")
	if err != nil {
		t.Fatalf("reading index sig: %v", err)
	}
	binaryBytes, err := os.ReadFile(probe)
	if err != nil {
		t.Fatalf("reading binary: %v", err)
	}
	const base = "https://github.invalid/registry"
	fetch := &fakeRepo{files: map[string][]byte{
		base + "/index.json":     indexBytes,
		base + "/index.json.sig": sigBytes,
		binURL:                   binaryBytes,
	}}

	reg := extension.NewRegistry()
	if err := reg.Add(extension.Repository{Name: "mosaic-official", URL: base, Key: ed25519.PublicKey(pub), Official: true}); err != nil {
		t.Fatalf("adding repo: %v", err)
	}
	inst := extension.NewInstaller(reg, fetch, t.TempDir())
	got, err := inst.Install(context.Background(), "mosaic-official", "extprobe")
	if err != nil {
		t.Fatalf("installing from the built repository: %v", err)
	}
	if got.Version != "v1.0.0" {
		t.Errorf("installed version: got %q", got.Version)
	}
}

func writeJSONFile(t *testing.T, path string, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshalling: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("writing %s: %v", path, err)
	}
}

// The tool refuses to sign a manifest that would not parse, so a publisher
// learns at signing time rather than the Platform learning at verify time far
// away.
func TestModulesignRefusesAnInvalidManifest(t *testing.T) {
	dir := t.TempDir()
	tool := buildModulesign(t, dir)

	keyPath := filepath.Join(dir, "key")
	run(t, tool, "genkey", "-out", keyPath)

	bad := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(bad, []byte(`{"schema":"wrong"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(tool, "sign", "-key", keyPath, bad)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("the tool signed an invalid manifest:\n%s", out)
	}
}

func buildModulesign(t *testing.T, dir string) string {
	t.Helper()
	bin := filepath.Join(dir, "modulesign")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/mosaic-media/platform/tools/modulesign")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building modulesign: %v\n%s", err, out)
	}
	return bin
}

func run(t *testing.T, tool string, args ...string) string {
	t.Helper()
	cmd := exec.Command(tool, args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("modulesign %v: %v\n%s", args, err, out)
	}
	return string(out)
}
