// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Manifest is an extension module's non-executing declaration (ADR 0065): the
// file the Platform reads to learn what a module is *without* running it.
//
// Reading it rather than executing the binary is the whole point. The
// alternative — running an unverified binary to ask it what it is — hands
// arbitrary code the Platform's authority before anything has been checked. So
// the manifest carries everything the Platform needs to decide whether to run
// the binary at all: who it claims to be, what it needs, and the digest of the
// bytes that claim goes with.
//
// It is a superset of the SDK's [v1.Manifest] (which a running module reports at
// handshake). The identity fields here become the DeclaredManifest that
// [Launch] then checks the running binary against, so a module is pinned three
// times over: the signature says the publisher stands behind this manifest, the
// digest says these bytes are the ones it stands behind, and the handshake says
// the running binary agrees with what the manifest claimed.
type Manifest struct {
	// Schema identifies the manifest format, for forward compatibility. A
	// Platform that does not recognise it refuses rather than guessing.
	Schema string `json:"schema"`
	// ID, Version and Name are the module's identity — the same fields the SDK
	// manifest carries, checked against the running binary at handshake.
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
	// SDKMajor is the SDK major version the module was built against (ADR 0064).
	// Compatibility is a shared major, so a mismatch is refused before the
	// process is even started — the one compatibility number a user reasons
	// about, checked without executing anything.
	SDKMajor int `json:"sdk_major"`
	// Provides are the roles the module fills (ADR 0027), declared here so a
	// selection or a capability-gated affordance can reason about the module
	// before it runs.
	Provides []v1.Role `json:"provides"`
	// Binaries is one entry per platform the module ships for, each carrying the
	// digest of that platform's binary. The Platform verifies the binary it is
	// about to run against the entry for its own platform.
	Binaries []BinaryRef `json:"binaries"`
}

// ManifestSchema is the only schema this Platform build understands.
const ManifestSchema = "mosaic.module.manifest/v1"

// BinaryRef is one platform's binary: where to fetch it, and the digest it must
// hash to.
type BinaryRef struct {
	OS   string `json:"os"`
	Arch string `json:"arch"`
	// Digest is the binary's content hash, "sha256:" followed by lowercase hex.
	// The prefix names the algorithm so a future one can be introduced without
	// ambiguity about what an old digest meant.
	Digest string `json:"digest"`
	// URL is where the binary is downloaded from. It lives in the manifest — the
	// module's own release — rather than being computed by the registry from a
	// template, because the module knows where it hosts its bytes and the
	// registry does not: a module's repository name need not match its id
	// (module-stremio-addons publishes the module whose id is "stremio"), so a
	// template keyed on the id would build the wrong URL. The module puts the
	// right one here once, at release.
	URL string `json:"url"`
}

// ParseManifest reads and validates a manifest's structure — not its signature
// or the binary, which are separate steps. It rejects a manifest whose schema
// this build does not know, and one missing an id, because those are the two
// ways a manifest is unusable regardless of whether it is authentic.
func ParseManifest(data []byte) (Manifest, error) {
	var m Manifest
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return Manifest{}, fmt.Errorf("extension: parsing manifest: %w", err)
	}
	if m.Schema != ManifestSchema {
		return Manifest{}, fmt.Errorf("extension: unknown manifest schema %q (this build understands %q)", m.Schema, ManifestSchema)
	}
	if m.ID == "" {
		return Manifest{}, fmt.Errorf("extension: manifest has no id")
	}
	return m, nil
}

// binaryFor returns the manifest's binary entry for one platform, or false when
// the module does not ship for it. A module absent for the Platform's own
// platform is refused rather than run — an arm64 NAS must not be handed an
// amd64 binary because the manifest happened to list one.
func (m Manifest) binaryFor(goos, goarch string) (BinaryRef, bool) {
	for _, b := range m.Binaries {
		if b.OS == goos && b.Arch == goarch {
			return b, true
		}
	}
	return BinaryRef{}, false
}

// toV1Manifest projects the identity fields onto the SDK manifest, so the
// distribution manifest and the handshake speak the same shape. The digests and
// schema stay behind — they are the install layer's business, not the running
// module's.
func (m Manifest) toV1Manifest() v1.Manifest {
	return v1.Manifest{
		ID:       m.ID,
		Version:  m.Version,
		Name:     m.Name,
		Provides: m.Provides,
	}
}

// FileDigest computes a binary's "sha256:hex" digest by streaming it, so a large
// binary is not read wholly into memory to be hashed. It is exported so the
// signing tool and this verifier compute the digest the same way — a divergence
// in the format string alone would make every signature fail to verify.
func FileDigest(path string) (string, error) {
	f, err := os.Open(path) //nolint:gosec // the path is the Platform's own install location.
	if err != nil {
		return "", fmt.Errorf("extension: opening binary to digest: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("extension: hashing binary: %w", err)
	}
	return "sha256:" + hex.EncodeToString(h.Sum(nil)), nil
}
