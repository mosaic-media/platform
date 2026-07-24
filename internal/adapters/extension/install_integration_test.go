// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build integration

package extension_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// TestRuntimeInstallFromOfficialRegistry is the whole extension pipeline, live
// and end to end, with zero compile-time dependency on any extension module.
//
// This is where the decoupling moved the "a real module runs out of process"
// proof (ADR 0079, ADR 0081): the platform module must not import an extension
// module, so instead of building one from an import, the Platform *installs* one
// at runtime the way a user would — fetching the signed index from the official
// registry, verifying it against the key compiled into this binary, downloading
// the platform's binary and checking its digest — then spawns it and confirms it
// serves. It imports the SDK and the Platform's own packages and nothing else.
//
// It is behind the `integration` build tag because it reaches the live registry
// (GitHub Pages) and GitHub releases; the default gate is hermetic and excludes
// it. A dedicated CI job runs `go test -tags integration ./...`.
func TestRuntimeInstallFromOfficialRegistry(t *testing.T) {
	// Every extension module the official registry catalogues, so this proves not
	// one module but the whole published set installs and — the risk slice 2.2
	// named — actually *serves* out of process: a stray stdout write, a global, or
	// an init that misbehaves corrupts the handshake, and only spawning the real
	// binary catches it. Each names one role it must declare, checked so a module
	// that came up empty is not counted as working.
	modules := []struct {
		id       string
		wantRole v1.Role
	}{
		{"stremio", v1.RoleMetadata},
		{"aiostreams", v1.RoleStream},
		{"fanart-tv", v1.RoleArtwork},
	}

	for _, mod := range modules {
		t.Run(mod.id, func(t *testing.T) {
			installer, err := extension.NewOfficialInstaller(t.TempDir())
			if err != nil {
				t.Fatalf("official installer: %v", err)
			}

			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			// Everything past the network fetch — the index's own signature, the
			// SDK major, a binary for this platform, and the downloaded binary's
			// digest against the signed manifest — is checked by Install before it
			// returns a launchable Config.
			installed, err := installer.Install(ctx, extension.OfficialRepositoryName, mod.id)
			if err != nil {
				t.Fatalf("install %s from the official registry: %v", mod.id, err)
			}
			if installed.ModuleID != mod.id {
				t.Fatalf("installed module id = %q, want %q", installed.ModuleID, mod.id)
			}
			if installed.Repository != extension.OfficialRepositoryName {
				t.Errorf("provenance = %q, want %q", installed.Repository, extension.OfficialRepositoryName)
			}

			// Spawn the verified binary and confirm it serves. The handshake makes
			// the final check ADR 0064 describes — the running binary agrees with
			// the manifest that was verified — so a successful Launch is that
			// agreement.
			cfg := installed.Config
			cfg.Content = stubContentService{}
			cfg.Telemetry = &recordingTelemetry{}
			m, err := extension.Launch(cfg)
			if err != nil {
				t.Fatalf("launch the installed %s: %v", mod.id, err)
			}
			t.Cleanup(m.Close)

			manifest := m.Capability.Manifest()
			if manifest.ID != mod.id {
				t.Errorf("running module id = %q, want %q", manifest.ID, mod.id)
			}
			declares := false
			for _, r := range manifest.Provides {
				if r == mod.wantRole {
					declares = true
					break
				}
			}
			if !declares {
				t.Errorf("running %s does not declare %q; roles = %v", mod.id, mod.wantRole, manifest.Provides)
			}
		})
	}
}
