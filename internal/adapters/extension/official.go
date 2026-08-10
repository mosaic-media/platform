// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"crypto/ed25519"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"fmt"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// officialPublicKey is Mosaic's own repository signing key, compiled into the
// binary (platform#40: the official repository is "trusted by default"). Trusted by
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
// repository. The URL is GitHub Pages for mosaic-media/registry (platform#50: the
// repository is signed static files over HTTPS, and GitHub is one untrusted
// host serving them).
const (
	OfficialRepositoryName = "mosaic-official"
	OfficialRepositoryURL  = "https://mosaic-media.github.io/registry"
)

// DevRepositoryURLEnv and DevRepositoryKeyEnv name the two variables a
// *development build* reads to point the official repository at a local one
// (platform#55): a registry the dev stack builds and signs from the sibling module
// checkouts, so a one-line change to an extension module is exercised through
// the real install path without a tag, a release and a registry publish.
//
// **This build ignores them unless it was built with the `mosaicdev` tag**, and
// the names are declared here — always compiled — precisely so that statement
// has somewhere to live and something to test. `devRepositoryOverride` is the
// only reader, and the untagged half of it (devregistry_off.go) reads no
// environment at all; the whole mechanism is absent from a shipped binary rather
// than switched off in one. See devregistry.go for why that is the guard rather
// than a runtime check.
const (
	DevRepositoryURLEnv = "MOSAIC_DEV_REPOSITORY_URL"
	DevRepositoryKeyEnv = "MOSAIC_DEV_REPOSITORY_KEY"
)

// devOverride is a development repository standing in for the official one: the
// base URL its index is fetched from, and the key that index is verified
// against. Both halves are replaced together and neither is optional — the
// point of the override is to exercise *verification* against a development
// key, so a URL without a key (or a key without a URL) is a configuration
// error, not a partial override.
//
// The type is declared here so both halves of devRepositoryOverride share one
// signature. Only the tagged half can ever produce a non-zero one.
type devOverride struct {
	URL string
	Key ed25519.PublicKey
}

// OfficialRepository returns Mosaic's own repository, trusted by default. It
// fails when the embedded key is not a well-formed ed25519 public key, which is
// a build fault worth catching at boot rather than at first install.
//
// In a development build (platform#55) the URL and key may be replaced from the
// environment. Everything downstream is unchanged: the index signature is
// verified, the manifests are authenticated by it, and each binary is checked
// against its signed digest. What changes is *whose* key vouches for the
// catalogue, which is the one thing a local loop has to be able to change. The
// returned repository is then not [Repository.Official] — it is demonstrably
// not Mosaic's — which is what the boot warning turns on.
func OfficialRepository() (Repository, error) {
	if len(officialPublicKey) != ed25519.PublicKeySize {
		return Repository{}, contracts.NewError(contracts.Internal, fmt.Sprintf(
			"extension: the embedded official key is %d bytes, not an ed25519 public key (%d)",
			len(officialPublicKey), ed25519.PublicKeySize))
	}
	repo := Repository{
		Name:     OfficialRepositoryName,
		URL:      OfficialRepositoryURL,
		Key:      ed25519.PublicKey(officialPublicKey),
		Official: true,
	}
	dev, overridden, err := devRepositoryOverride()
	if err != nil {
		return Repository{}, err
	}
	if overridden {
		repo.URL = dev.URL
		repo.Key = dev.Key
		repo.Official = false
	}
	return repo, nil
}

// KeyFingerprint is a short, stable name for the key a repository is verified
// against — the SHA-256 of the public key, truncated. It exists for the boot
// log: "which key is this Platform trusting" is the question a development
// override makes worth answering out loud, and printing the key itself answers
// it in a form nobody can read or compare at a glance.
func (r Repository) KeyFingerprint() string {
	if len(r.Key) == 0 {
		return ""
	}
	sum := sha256.Sum256(r.Key)
	return "sha256:" + hex.EncodeToString(sum[:8])
}

// DefaultRegistry returns a registry trusting only the official repository — the
// trust a fresh install has before a user adds any third-party one (platform#40).
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
//
// The fetcher comes from [officialFetcher] rather than being named here, because
// a development override (platform#55) needs a different one: a local registry is
// on a private address, which is exactly what the dial guard refuses. That
// relaxation is the sharper half of the override and is compiled out of a
// shipped binary along with the rest of it.
func NewOfficialInstaller(dir string) (*Installer, error) {
	reg, err := DefaultRegistry()
	if err != nil {
		return nil, err
	}
	return NewInstaller(reg, officialFetcher(), dir), nil
}
