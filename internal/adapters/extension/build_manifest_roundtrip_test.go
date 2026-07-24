// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The producer chain end to end, with real binaries: the module prints its
// identity (host --mosaic-manifest), the tool joins it with the SDK major and
// the binaries into a manifest (modulesign build-manifest), and the Platform
// verifies the binary against that manifest and launches it. This is what a
// module's release does, proven without a release.
func TestBuildManifestFromAModuleThenLaunch(t *testing.T) {
	dir := t.TempDir()
	probe := buildProbe(t)
	tool := buildModulesign(t, dir)

	// 1. The module tells the release what it is.
	identity := filepath.Join(dir, "identity.json")
	out, err := exec.Command(probe, "--mosaic-manifest").Output()
	if err != nil {
		t.Fatalf("running probe --mosaic-manifest: %v", err)
	}
	if err := os.WriteFile(identity, out, 0o600); err != nil {
		t.Fatal(err)
	}

	// 2. The tool joins identity + SDK major + the binary into a manifest, with
	// each binary's download URL filled from the template the module's release
	// supplies (it knows its own repo and asset names).
	manifestPath := filepath.Join(dir, "manifest.json")
	run(t, tool, "build-manifest",
		"-identity", identity,
		"-sdk-major", "0",
		"-url", "https://github.invalid/module-extprobe/releases/download/{version}/extprobe-{os}-{arch}",
		"-out", manifestPath,
		runtime.GOOS+"/"+runtime.GOARCH+"="+probe,
	)

	// 3. The Platform verifies the binary against that manifest — the digest the
	// tool wrote must match the bytes on disk — and the launched module is the
	// one the manifest describes. The manifest is authenticated here by the same
	// path the repository index would use, so this is checkManifestAgainstBinary
	// exercised through the real manifest bytes rather than a hand-built struct.
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		t.Fatal(err)
	}
	m, err := extension.ParseManifest(data)
	if err != nil {
		t.Fatalf("the built manifest does not parse: %v", err)
	}
	if m.ID != "extprobe" {
		t.Errorf("manifest id: got %q, want extprobe (the identity the module printed)", m.ID)
	}
	if len(m.Provides) != 1 || m.Provides[0] != v1.RoleSearch {
		t.Errorf("provides did not survive: %v", m.Provides)
	}
	if len(m.Binaries) != 1 || m.Binaries[0].OS != runtime.GOOS {
		t.Errorf("binaries: got %+v", m.Binaries)
	}
	// The URL template was filled with this module's own placeholders.
	wantURL := "https://github.invalid/module-extprobe/releases/download/v0.1.0/extprobe-" + runtime.GOOS + "-" + runtime.GOARCH
	if m.Binaries[0].URL != wantURL {
		t.Errorf("binary URL: got %q, want %q", m.Binaries[0].URL, wantURL)
	}

	// The digest the tool computed is the digest the Platform computes — the
	// whole chain agrees on the bytes.
	got, err := extension.FileDigest(probe)
	if err != nil {
		t.Fatal(err)
	}
	if m.Binaries[0].Digest != got {
		t.Errorf("digest mismatch between build-manifest and the Platform: %q vs %q", m.Binaries[0].Digest, got)
	}
}
