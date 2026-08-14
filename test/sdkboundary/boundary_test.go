// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package sdkboundary holds the standing enforcement of platform#12: the
// published contract surface must be importable and self-contained for a
// Module built outside the Platform's module.
package sdkboundary_test

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestPublishedSurfaceCompilesFromAnExternalModule builds test/sdkprobe, a
// separate Go module that imports only contracts/platform/v1. Go forbids an
// external module from importing anything under internal/, so if any public
// signature in v1 referenced an internal type — a leak of Platform plumbing
// into the SDK — v1 would import internal/ and this build would fail with
// "use of internal package". A green build is the proof the surface is clean.
//
// This replaces the manual probe that first found the internal/ barrier with
// a check the suite runs on every change.
func TestPublishedSurfaceCompilesFromAnExternalModule(t *testing.T) {
	if _, err := exec.LookPath("go"); err != nil {
		t.Skip("go toolchain not on PATH")
	}

	dir, err := filepath.Abs(filepath.Join("..", "sdkprobe"))
	if err != nil {
		t.Fatalf("resolve probe dir: %v", err)
	}

	cmd := exec.Command("go", "build", "./...")
	cmd.Dir = dir

	// The probe is a separate module on purpose, and it resolves the SDK through
	// its own `replace` — standing in for the proxy a third party resolves from.
	// An ambient go.work supersedes that, so the probe would be built against
	// whatever the workspace happened to hold instead of against the SDK it
	// names. Worse, the probe is not one of a workspace's modules, so `./...`
	// matches nothing and the build fails with a pattern error — reported here as
	// a leak of an internal type, which is the one diagnosis it is not.
	//
	// Two things above this tree already write a go.work: the dev overlay
	// (docker-compose.local.yml) and the cross-repository graph workflow. The
	// isolation this test needs has to be asked for rather than assumed.
	cmd.Env = append(os.Environ(), "GOWORK=off")

	out, err := cmd.CombinedOutput()
	if err == nil {
		return
	}

	// **Say which failure this is.** The message used to report every build
	// failure as an internal-type leak, which is the one diagnosis most of them
	// are not: a missing go.sum entry reported as "an internal type has leaked
	// into the published surface" sends the reader to look for a leak that was
	// never there. The comment above already names the same trap for an ambient
	// go.work; this makes the distinction the test's own rather than a comment's.
	if bytes.Contains(out, []byte("use of internal package")) {
		t.Fatalf("an internal type has leaked into the published surface (platform#12) "+
			"— an external module cannot import it:\n%s", out)
	}
	t.Fatalf("the probe failed to build, and NOT because of an internal-type leak — "+
		"no 'use of internal package' in the output. A missing go.sum entry means the "+
		"SDK's requirements moved and test/sdkprobe's go.mod/go.sum need `go mod tidy` "+
		"in the container:\n%s", out)
}
