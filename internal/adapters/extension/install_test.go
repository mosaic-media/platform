// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"runtime"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// signedProbe builds the probe, writes a manifest that vouches for its real
// digest, signs the manifest with a fresh key, and returns everything a
// verification needs. It is the honest round trip: what is verified is the same
// bytes that were signed and the same binary that was hashed.
type signedProbe struct {
	manifest   []byte
	signature  []byte
	binaryPath string
	keyring    *extension.Keyring
	pub        ed25519.PublicKey
	priv       ed25519.PrivateKey
}

func newSignedProbe(t *testing.T, mutate func(m *manifestJSON)) signedProbe {
	t.Helper()

	bin := buildProbe(t)
	digest := digestOf(t, bin)

	m := manifestJSON{
		Schema:   extension.ManifestSchema,
		ID:       "extprobe",
		Version:  "v0.1.0",
		Name:     "Extension Probe",
		SDKMajor: 0, // must match host.SDKMajor
		Provides: []string{string(v1.RoleSearch)},
		Binaries: []binaryJSON{{OS: runtime.GOOS, Arch: runtime.GOARCH, Digest: digest}},
	}
	if mutate != nil {
		mutate(&m)
	}
	manifestBytes, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshalling manifest: %v", err)
	}

	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatalf("generating key: %v", err)
	}
	kr := extension.NewKeyring()
	if err := kr.Trust("test-publisher", pub); err != nil {
		t.Fatalf("trusting key: %v", err)
	}

	return signedProbe{
		manifest:   manifestBytes,
		signature:  ed25519.Sign(priv, manifestBytes),
		binaryPath: bin,
		keyring:    kr,
		pub:        pub,
		priv:       priv,
	}
}

// manifestJSON mirrors the manifest wire shape, so a test can build and mutate
// one without the package exporting its JSON tags for construction.
type manifestJSON struct {
	Schema   string       `json:"schema"`
	ID       string       `json:"id"`
	Version  string       `json:"version"`
	Name     string       `json:"name"`
	SDKMajor int          `json:"sdk_major"`
	Provides []string     `json:"provides"`
	Binaries []binaryJSON `json:"binaries"`
}

type binaryJSON struct {
	OS     string `json:"os"`
	Arch   string `json:"arch"`
	Digest string `json:"digest"`
}

func digestOf(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading binary: %v", err)
	}
	sum := sha256.Sum256(data)
	return "sha256:" + hex.EncodeToString(sum[:])
}

func mustCategory(t *testing.T, err error, want contracts.ErrorCategory) {
	t.Helper()
	if err == nil {
		t.Fatal("expected an error")
	}
	if got := contracts.CategoryOf(err); got != want {
		t.Errorf("category: got %q, want %q (%v)", got, want, err)
	}
}

// The whole chain: a signed manifest, a matching digest, the right SDK major, a
// binary for this platform. Verify returns a Config that then launches, and the
// running module's manifest agrees with what was signed.
func TestVerifyAcceptsASignedMatchingModule(t *testing.T) {
	sp := newSignedProbe(t, nil)

	v, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, sp.keyring)
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if v.SignedBy != "test-publisher" {
		t.Errorf("provenance: got %q, want test-publisher", v.SignedBy)
	}
	if v.Config.DeclaredManifest.ID != "extprobe" {
		t.Errorf("declared manifest id: got %q", v.Config.DeclaredManifest.ID)
	}

	// The verified Config launches, and the handshake makes the third check:
	// the running binary agrees with the signed manifest.
	m, err := extension.Launch(v.Config)
	if err != nil {
		t.Fatalf("launching a verified module: %v", err)
	}
	t.Cleanup(m.Close)
	if m.Capability.Manifest().ID != "extprobe" {
		t.Errorf("running manifest id: got %q", m.Capability.Manifest().ID)
	}
}

// A tampered manifest — the attacker changed a byte after signing — no longer
// verifies. This is the property the whole scheme rests on.
func TestVerifyRejectsATamperedManifest(t *testing.T) {
	sp := newSignedProbe(t, nil)
	tampered := append([]byte(nil), sp.manifest...)
	tampered[len(tampered)/2] ^= 0xff

	_, err := extension.Verify(tampered, sp.signature, sp.binaryPath, sp.keyring)
	mustCategory(t, err, contracts.PermissionDenied)
}

// A manifest signed by a key the Platform does not trust is refused, even though
// it is validly signed. Signing is universal; trust is about whose key.
func TestVerifyRejectsAnUntrustedSigner(t *testing.T) {
	sp := newSignedProbe(t, nil)

	// A different, untrusted keyring.
	otherPub, _, _ := ed25519.GenerateKey(rand.Reader)
	untrusting := extension.NewKeyring()
	_ = untrusting.Trust("someone-else", otherPub)

	_, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, untrusting)
	mustCategory(t, err, contracts.PermissionDenied)
}

// An empty keyring refuses everything: with no trusted publisher, nothing can be
// run.
func TestVerifyRefusesWithNoTrustedKeys(t *testing.T) {
	sp := newSignedProbe(t, nil)
	_, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, extension.NewKeyring())
	mustCategory(t, err, contracts.PermissionDenied)
}

// The manifest is authentic but the binary on disk is not the one it vouches
// for — a swapped binary after signing. The digest check catches it.
func TestVerifyRejectsADigestMismatch(t *testing.T) {
	sp := newSignedProbe(t, func(m *manifestJSON) {
		// Declare a digest that is valid-looking but not the binary's.
		m.Binaries[0].Digest = "sha256:" + hex.EncodeToString(make([]byte, 32))
	})
	_, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, sp.keyring)
	mustCategory(t, err, contracts.PermissionDenied)
}

// A module built against a different SDK major is refused before it is run — the
// one compatibility number, checked without executing anything (platform#39).
func TestVerifyRejectsAnIncompatibleSDKMajor(t *testing.T) {
	sp := newSignedProbe(t, func(m *manifestJSON) { m.SDKMajor = 99 })
	_, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, sp.keyring)
	mustCategory(t, err, contracts.InvalidArgument)
}

// A module that ships no binary for this platform is refused rather than handed
// some other platform's binary.
func TestVerifyRejectsAModuleWithNoBinaryForThisPlatform(t *testing.T) {
	sp := newSignedProbe(t, func(m *manifestJSON) {
		m.Binaries = []binaryJSON{{OS: "plan9", Arch: "sparc64", Digest: "sha256:x"}}
	})
	_, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, sp.keyring)
	mustCategory(t, err, contracts.Unavailable)
}

// An unknown manifest schema is refused rather than guessed at.
func TestVerifyRejectsAnUnknownSchema(t *testing.T) {
	sp := newSignedProbe(t, func(m *manifestJSON) { m.Schema = "something.else/v9" })
	_, err := extension.Verify(sp.manifest, sp.signature, sp.binaryPath, sp.keyring)
	mustCategory(t, err, contracts.InvalidArgument)
}

// VerifyFiles reads the three artefacts from disk — the shape a download
// produces — and reaches the same verdict.
func TestVerifyFilesReadsFromDisk(t *testing.T) {
	sp := newSignedProbe(t, nil)
	dir := t.TempDir()
	manifestPath := dir + "/manifest.json"
	sigPath := dir + "/manifest.sig"
	if err := os.WriteFile(manifestPath, sp.manifest, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sigPath, sp.signature, 0o600); err != nil {
		t.Fatal(err)
	}

	v, err := extension.VerifyFiles(manifestPath, sigPath, sp.binaryPath, sp.keyring)
	if err != nil {
		t.Fatalf("verify files: %v", err)
	}
	if v.SignedBy != "test-publisher" {
		t.Errorf("provenance: got %q", v.SignedBy)
	}
}
