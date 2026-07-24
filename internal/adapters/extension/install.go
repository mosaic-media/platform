// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"crypto/subtle"
	"fmt"
	"os"
	"runtime"

	"github.com/mosaic-media/sdk/host"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Verify checks a downloaded extension module before it is ever run, and is the
// gate ADR 0065 requires between an artefact and the Platform's authority. Under
// ADR 0079 it is the Platform that runs this, not the Supervisor: the process
// that will spawn the module and hand it a Caller is the one that checks it.
//
// It refuses on the first failure and the checks run in the order that fails
// cheapest and most decisively first — an unsigned or untrusted manifest is
// refused before its bytes are parsed as configuration, and the running
// platform's binary is only hashed once the manifest it would be checked against
// is known to be authentic.
//
//  1. **Signature.** The manifest is signed by a trusted publisher key
//     (Keyring). An unsigned or untrusted-key manifest is refused — this is the
//     "signing is universal, trust is about whose key" line from ADR 0065.
//  2. **Schema and identity.** The manifest parses and names a module.
//  3. **SDK major.** The module was built against this Platform's SDK major
//     (ADR 0064); a mismatch is refused here, without executing anything.
//  4. **Platform.** The module ships a binary for this OS and architecture.
//  5. **Digest.** The binary on disk hashes to the digest the (now-authentic)
//     manifest declares for this platform.
//
// On success it returns a [Config] ready for [Launch] or [Supervise], carrying
// the manifest's identity as DeclaredManifest — so the handshake then makes the
// third check ADR 0064 describes, that the running binary agrees with what was
// signed. The verifying publisher is returned as provenance.
type Verified struct {
	Config   Config
	SignedBy string // the trusted publisher whose key verified the manifest
}

// Verify runs the checks for the running platform. The manifest bytes and its
// detached signature are the signed pair; binaryPath is the binary the manifest
// vouches for.
func Verify(manifest, signature []byte, binaryPath string, keyring *Keyring) (Verified, error) {
	return verifyFor(manifest, signature, binaryPath, keyring, runtime.GOOS, runtime.GOARCH)
}

// verifyFor is Verify with the target platform made explicit, so a test can
// verify for a platform it is not running on. A cross-built module is verified
// against the platform it will run on, which is always the Platform's own.
func verifyFor(manifest, signature []byte, binaryPath string, keyring *Keyring, goos, goarch string) (Verified, error) {
	if keyring == nil || keyring.empty() {
		return Verified{}, contracts.NewError(contracts.PermissionDenied,
			"extension: no trusted keys; every module must be signed by a trusted publisher")
	}

	// 1. Signature, before the manifest is trusted as configuration.
	signer, ok := keyring.verify(manifest, signature)
	if !ok {
		return Verified{}, contracts.NewError(contracts.PermissionDenied,
			"extension: manifest signature does not verify against any trusted key")
	}

	// 2. Schema and identity.
	m, err := ParseManifest(manifest)
	if err != nil {
		return Verified{}, contracts.WrapError(contracts.InvalidArgument, "extension: manifest", err)
	}

	// 3-5. The manifest is authentic; check it against the binary. This step is
	// shared with the repository-index install path (installer.go), where the
	// index's own signature authenticates the manifest instead of a detached
	// one — so authentication and the binary check are separate functions.
	cfg, err := checkManifestAgainstBinary(m, binaryPath, goos, goarch)
	if err != nil {
		return Verified{}, err
	}
	return Verified{Config: cfg, SignedBy: signer}, nil
}

// checkManifestAgainstBinary runs the checks that do not depend on how the
// manifest was authenticated: the SDK major, a binary for this platform, and the
// digest. It is called once the manifest is known to be authentic — by a
// detached signature ([Verify]) or by a signed repository index ([Installer]).
func checkManifestAgainstBinary(m Manifest, binaryPath, goos, goarch string) (Config, error) {
	// SDK major, refused without executing anything.
	if m.SDKMajor != host.SDKMajor {
		return Config{}, contracts.NewError(contracts.InvalidArgument, fmt.Sprintf(
			"extension: module %q was built against SDK major %d, this Platform speaks %d",
			m.ID, m.SDKMajor, host.SDKMajor))
	}

	// A binary for this platform.
	ref, ok := m.binaryFor(goos, goarch)
	if !ok {
		return Config{}, contracts.NewError(contracts.Unavailable, fmt.Sprintf(
			"extension: module %q ships no binary for %s/%s", m.ID, goos, goarch))
	}

	// Digest of the actual bytes against what the authentic manifest declares.
	got, err := FileDigest(binaryPath)
	if err != nil {
		return Config{}, contracts.WrapError(contracts.Unavailable, "extension: digesting binary", err)
	}
	// Constant-time compare: a digest check is a security decision, and a
	// timing-variable string compare leaks how much of a forged digest matched.
	if subtle.ConstantTimeCompare([]byte(got), []byte(ref.Digest)) != 1 {
		return Config{}, contracts.NewError(contracts.PermissionDenied, fmt.Sprintf(
			"extension: binary for %q does not match the signed digest (declared %s, got %s)",
			m.ID, ref.Digest, got))
	}

	return Config{BinaryPath: binaryPath, DeclaredManifest: m.toV1Manifest()}, nil
}

// VerifyFiles is Verify reading the manifest, signature and binary from disk —
// the shape a download produces: a directory holding the three. It exists so a
// caller with paths does not have to read the two small files itself, while
// Verify stays testable with bytes in hand.
func VerifyFiles(manifestPath, signaturePath, binaryPath string, keyring *Keyring) (Verified, error) {
	manifest, err := os.ReadFile(manifestPath) //nolint:gosec // the path is the Platform's own install location.
	if err != nil {
		return Verified{}, contracts.WrapError(contracts.Unavailable, "extension: reading manifest", err)
	}
	signature, err := os.ReadFile(signaturePath) //nolint:gosec // as above.
	if err != nil {
		return Verified{}, contracts.WrapError(contracts.Unavailable, "extension: reading signature", err)
	}
	return Verify(manifest, signature, binaryPath, keyring)
}
