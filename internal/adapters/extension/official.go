// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"crypto/ed25519"
	_ "embed"
	"fmt"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// officialPublicKey is Mosaic's own repository signing key, compiled into the
// binary (ADR 0065: the official repository is "trusted by default"). Trusted by
// default means exactly this — baked in, not configured — so a user who never
// touches repository settings still verifies every official module against the
// key Mosaic released it under. Its private half is the registry's CI secret and
// exists nowhere in this repository.
//
// It is the public half, so embedding it is safe: it authenticates a download,
// it does not authorise anything. A build with a corrupt or wrong key here fails
// at composition ([DefaultRegistry]) rather than silently trusting nothing or
// the wrong publisher.
//
//go:embed mosaic-official.pub
var officialPublicKey []byte

// OfficialRepositoryName and OfficialRepositoryURL identify Mosaic's own
// repository. The URL is GitHub Pages for mosaic-media/registry (ADR 0080: the
// repository is signed static files over HTTPS, and GitHub is one untrusted
// host serving them).
const (
	OfficialRepositoryName = "mosaic-official"
	OfficialRepositoryURL  = "https://mosaic-media.github.io/registry"
)

// OfficialRepository returns Mosaic's own repository, trusted by default. It
// fails when the embedded key is not a well-formed ed25519 public key, which is
// a build fault worth catching at boot rather than at first install.
func OfficialRepository() (Repository, error) {
	if len(officialPublicKey) != ed25519.PublicKeySize {
		return Repository{}, contracts.NewError(contracts.Internal, fmt.Sprintf(
			"extension: the embedded official key is %d bytes, not an ed25519 public key (%d)",
			len(officialPublicKey), ed25519.PublicKeySize))
	}
	return Repository{
		Name:     OfficialRepositoryName,
		URL:      OfficialRepositoryURL,
		Key:      ed25519.PublicKey(officialPublicKey),
		Official: true,
	}, nil
}

// DefaultRegistry returns a registry trusting only the official repository — the
// trust a fresh install has before a user adds any third-party one (ADR 0065).
// The composition root builds this, so a corrupt embedded key fails the boot.
func DefaultRegistry() (*Registry, error) {
	repo, err := OfficialRepository()
	if err != nil {
		return nil, err
	}
	reg := NewRegistry()
	if err := reg.Add(repo); err != nil {
		return nil, err
	}
	return reg, nil
}

// NewOfficialInstaller builds an installer trusting the official repository,
// fetching over HTTPS through netguard's dial guard, and storing modules under
// dir. It is what an install trigger (an admin action, when that surface exists)
// uses to bring a module down and verify it before it runs.
func NewOfficialInstaller(dir string) (*Installer, error) {
	reg, err := DefaultRegistry()
	if err != nil {
		return nil, err
	}
	return NewInstaller(reg, NewHTTPFetcher(), dir), nil
}
