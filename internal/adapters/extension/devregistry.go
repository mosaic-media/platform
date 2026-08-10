// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

//go:build mosaicdev

// The development half of the module-repository override (platform#55). **This
// file is compiled only into a build made with `-tags mosaicdev`**; the shipped
// binary gets devregistry_off.go instead, which reads no environment and can
// return no override.
//
// The build tag *is* the guard, and it is deliberately not a runtime one. An
// environment variable that repoints the module repository is a remote-code-
// execution path: the repository's key vouches for every binary the Platform
// downloads and spawns with its own authority, so anything able to name a
// different key and URL can have the Platform run whatever it likes. A runtime
// check — a flag, an "are you sure" variable, a warning nobody reads — leaves
// that path present in every release, one environment edit away, on a surface
// (a NAS app's env table, a hosting panel, a compose file pasted from a forum)
// where editing configuration is not supposed to mean executing code. There is
// no check that makes the mechanism safe to ship, so the mechanism is not
// shipped: `go build ./...` produces a binary in which none of this exists —
// not the parser, not the environment read, and not the unguarded fetcher
// below.
//
// What that costs is real and worth stating: the dev stack runs a binary built
// differently from the released one. It is confined to *which repository and
// key are trusted* and *which dialer fetches from it*. Every check that decides
// whether a module runs — the index signature, the manifest, the SDK major, the
// per-platform binary digest, the handshake — is the same code on both sides of
// the tag. A development key signs a development index, and the local loop
// exercises the real verification path rather than a bypass of it.
package extension

import (
	"crypto/ed25519"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// devRepositoryOverride reads the override from the environment. Both variables
// must be set or neither: a URL with no key would have to fall back to the
// official key, which is a repository nobody can sign for, and a key with no URL
// silently trusts a second key for Mosaic's own index — a strictly worse
// position than either the default or the intended override. Half-configured is
// therefore a boot failure, not a fallback, because the failure a fallback
// produces is "my local module did not appear" answered three layers away.
func devRepositoryOverride() (devOverride, bool, error) {
	url, keyPath := os.Getenv(DevRepositoryURLEnv), os.Getenv(DevRepositoryKeyEnv)
	switch {
	case url == "" && keyPath == "":
		return devOverride{}, false, nil
	case url == "":
		return devOverride{}, false, contracts.NewError(contracts.InvalidArgument, fmt.Sprintf(
			"extension: %s is set without %s; a development repository must supply both its URL and the key its index is signed with",
			DevRepositoryKeyEnv, DevRepositoryURLEnv))
	case keyPath == "":
		return devOverride{}, false, contracts.NewError(contracts.InvalidArgument, fmt.Sprintf(
			"extension: %s is set without %s; signature verification is not optional, so a development repository must name the key its index is signed with",
			DevRepositoryURLEnv, DevRepositoryKeyEnv))
	}

	// The key arrives as a file path, never as the key's bytes and never over
	// the network. A key fetched from the repository it is meant to authenticate
	// would verify every index that repository ever served, which is no
	// verification at all; a path is something the operator of the stack put
	// there out of band, which is what the compiled-in key is too.
	key, err := os.ReadFile(keyPath) //nolint:gosec // a development build, reading the path the developer named.
	if err != nil {
		return devOverride{}, false, contracts.WrapError(contracts.InvalidArgument, fmt.Sprintf(
			"extension: reading the development repository key named by %s", DevRepositoryKeyEnv), err)
	}
	if len(key) != ed25519.PublicKeySize {
		return devOverride{}, false, contracts.NewError(contracts.InvalidArgument, fmt.Sprintf(
			"extension: the development repository key at %s is %d bytes, not an ed25519 public key (%d) — modulesign genkey writes the raw key",
			keyPath, len(key), ed25519.PublicKeySize))
	}
	return devOverride{URL: url, Key: ed25519.PublicKey(key)}, true, nil
}

// officialFetcher returns the fetcher the official installer downloads through:
// the guarded one, unless a development repository is configured.
//
// A local registry is on a private address — a compose network, a host
// loopback — and netguard refuses exactly those (that is what it is for: an
// install is the Platform fetching a URL on a user's behalf, the SSRF shape the
// guard exists to close). So an override that could not also relax the dialer
// would fetch nothing and the loop would not exist. This is the more dangerous
// of the two relaxations and the better argument for the build tag: it is not
// merely "trust a different key", it is "reach the host's own network", and a
// shipped binary contains no code that can.
//
// An override that fails to parse leaves the guarded fetcher in place. Nothing
// downstream reaches this state — [OfficialRepository] has already failed the
// boot on the same error — and the safe branch is the right one to take if it
// ever did.
func officialFetcher() Fetcher {
	if _, overridden, err := devRepositoryOverride(); err != nil || !overridden {
		return NewHTTPFetcher()
	}
	return &httpFetcher{
		client: &http.Client{
			Timeout: 60 * time.Second,
			Transport: &http.Transport{
				TLSHandshakeTimeout: 10 * time.Second,
			},
		},
	}
}
