// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Installer downloads, verifies and stores an extension module from a trusted
// repository (ADR 0065, Platform-side per ADR 0079). It is what feeds the
// verification gate: a repository index and a binary come off the network, and
// an [Installed] record that [Launch] can run comes out — with the provenance
// ADR 0065 requires kept alongside.
//
// It grants no authority and skips no check. A module reaches an [Installed]
// only if its repository's index signature verifies, its inline manifest is for
// this Platform's SDK major and platform, and the downloaded binary hashes to
// the digest that signed catalogue declared.
type Installer struct {
	registry *Registry
	fetch    Fetcher
	dir      string
}

// Fetcher retrieves bytes from a URL. Production wires an HTTPS client that
// routes through netguard's dial guard, the same protection every other
// outbound Platform fetch uses; a test wires a fake. It is an interface so the
// install flow is testable without a live repository and so the one place that
// reaches the network is explicit.
type Fetcher interface {
	Fetch(ctx context.Context, url string) ([]byte, error)
}

// Installed is a verified, on-disk module and where it came from. The
// provenance — which repository, verified against whose key — stays with it, so
// the module list, its settings page and an admin looking at a broken import can
// all show where it came from (ADR 0065). A consent dialog clicked months ago is
// not context; this is.
type Installed struct {
	ModuleID   string
	Version    string
	Repository string // provenance: the repository this module was installed from
	Config     Config // ready for Launch or Supervise
}

// NewInstaller returns an installer that stores modules under dir. dir is the
// Platform's own install location; a module's binary and manifest land in a
// per-module subdirectory of it.
func NewInstaller(registry *Registry, fetch Fetcher, dir string) *Installer {
	return &Installer{registry: registry, fetch: fetch, dir: dir}
}

// Install fetches, verifies and stores one module from one repository. It is the
// whole flow, and it refuses at the first check that fails.
func (i *Installer) Install(ctx context.Context, repoName, moduleID string) (Installed, error) {
	return i.installFor(ctx, repoName, moduleID, runtime.GOOS, runtime.GOARCH)
}

func (i *Installer) installFor(ctx context.Context, repoName, moduleID, goos, goarch string) (Installed, error) {
	repo, ok := i.registry.Lookup(repoName)
	if !ok {
		return Installed{}, contracts.NewError(contracts.NotFound,
			fmt.Sprintf("extension: no repository named %q is configured", repoName))
	}

	// The index and its detached signature. Fetching the signature separately
	// keeps the signed bytes exactly the index bytes.
	indexData, err := i.fetch.Fetch(ctx, repo.URL+"/index.json")
	if err != nil {
		return Installed{}, contracts.WrapError(contracts.Unavailable, "extension: fetching index", err)
	}
	indexSig, err := i.fetch.Fetch(ctx, repo.URL+"/index.json.sig")
	if err != nil {
		return Installed{}, contracts.WrapError(contracts.Unavailable, "extension: fetching index signature", err)
	}

	// The index is signed by the repository — "this catalogue came from the
	// repository unaltered" — which authenticates every inline manifest at once.
	idx, err := repo.verifyIndex(indexData, indexSig)
	if err != nil {
		return Installed{}, err
	}

	entry, ok := idx.module(moduleID)
	if !ok {
		return Installed{}, contracts.NewError(contracts.NotFound,
			fmt.Sprintf("extension: repository %q offers no module %q", repoName, moduleID))
	}

	ref, ok := entry.Manifest.binaryFor(goos, goarch)
	if !ok {
		return Installed{}, contracts.NewError(contracts.Unavailable,
			fmt.Sprintf("extension: module %q offers no binary for %s/%s", moduleID, goos, goarch))
	}
	if ref.URL == "" {
		return Installed{}, contracts.NewError(contracts.Unavailable,
			fmt.Sprintf("extension: module %q names no download URL for %s/%s", moduleID, goos, goarch))
	}

	// Download the binary and store it. The store happens before the digest
	// check so the check runs against the bytes that actually landed on disk,
	// not against a copy in memory that a later write could diverge from.
	binaryData, err := i.fetch.Fetch(ctx, resolveURL(repo.URL, ref.URL))
	if err != nil {
		return Installed{}, contracts.WrapError(contracts.Unavailable, "extension: fetching binary", err)
	}

	moduleDir := filepath.Join(i.dir, safeName(moduleID))
	if err := os.MkdirAll(moduleDir, 0o755); err != nil { //nolint:gosec // an install directory, not a secret.
		return Installed{}, contracts.WrapError(contracts.Internal, "extension: creating install dir", err)
	}
	binaryPath := filepath.Join(moduleDir, "module")
	// 0o755: the binary must be executable to be spawned.
	if err := os.WriteFile(binaryPath, binaryData, 0o755); err != nil { //nolint:gosec // an executable module, by design.
		return Installed{}, contracts.WrapError(contracts.Internal, "extension: writing binary", err)
	}

	// The manifest was authenticated by the index signature; now the binary on
	// disk is checked against the digest that signed manifest declared. A
	// tampered mirror that served different bytes than the repository signed for
	// is caught here.
	cfg, err := checkManifestAgainstBinary(entry.Manifest, binaryPath, goos, goarch)
	if err != nil {
		// A binary that fails the check must not be left on disk to be run by a
		// later path that trusts the directory's existence.
		_ = os.Remove(binaryPath)
		return Installed{}, err
	}

	return Installed{
		ModuleID:   entry.Manifest.ID,
		Version:    entry.Manifest.Version,
		Repository: repo.Name,
		Config:     cfg,
	}, nil
}

// resolveURL joins a possibly-relative binary URL to the repository base. An
// absolute URL is used as-is, so a repository may serve its binaries from a CDN
// on another host; a relative one is under the repository.
func resolveURL(base, u string) string {
	if strings.HasPrefix(u, "http://") || strings.HasPrefix(u, "https://") {
		return u
	}
	return strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(u, "/")
}

// safeName strips a module id to a filesystem-safe directory name, so a
// malicious id cannot escape the install directory with path separators. Module
// ids are simple identifiers, so this only ever changes a hostile one.
func safeName(id string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, id)
}
