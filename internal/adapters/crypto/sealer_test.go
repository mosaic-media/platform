// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package crypto_test

import (
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
)

func TestSealRoundTrips(t *testing.T) {
	sealer := crypto.NewSecretSealer([]byte("an install key"))

	sealed, err := sealer.Seal("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	opened, err := sealer.Open(sealed)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if opened != "JBSWY3DPEHPK3PXP" {
		t.Errorf("opened %q", opened)
	}
}

// The secret must not be readable in the stored value. That is the whole
// defence: a backup, a replica or a dump carries the table and not the key.
func TestTheSecretIsNotRecoverableFromTheSealedValue(t *testing.T) {
	const secret = "JBSWY3DPEHPK3PXP"
	sealer := crypto.NewSecretSealer([]byte("an install key"))

	sealed, err := sealer.Seal(secret)
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if strings.Contains(sealed, secret) {
		t.Fatalf("the sealed value contains the secret verbatim: %q", sealed)
	}
	// And a different key does not open it — the property that makes taking
	// the database without the key worthless.
	other := crypto.NewSecretSealer([]byte("a different install"))
	if _, err := other.Open(sealed); err == nil {
		t.Fatal("a value sealed under one key opened under another")
	}
}

// Sealing is randomised per call, so two users with the same secret do not
// produce the same row — otherwise the table would leak which accounts share a
// factor, and a rainbow table over common values would work.
func TestSealingIsRandomised(t *testing.T) {
	sealer := crypto.NewSecretSealer([]byte("an install key"))

	first, err := sealer.Seal("same")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	second, err := sealer.Seal("same")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if first == second {
		t.Fatal("sealing the same value twice produced identical output")
	}
}

// A tampered row must fail to open rather than decrypt to something plausible.
// GCM authenticates, and this is what says so.
func TestATamperedValueDoesNotOpen(t *testing.T) {
	sealer := crypto.NewSecretSealer([]byte("an install key"))
	sealed, err := sealer.Seal("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}

	// Flip a character in the body, leaving the envelope intact.
	body := []byte(sealed)
	idx := len(body) - 3
	if body[idx] == 'A' {
		body[idx] = 'B'
	} else {
		body[idx] = 'A'
	}
	if _, err := sealer.Open(string(body)); err == nil {
		t.Fatal("an altered value opened")
	}
}

// The envelope is what makes a later key or algorithm change possible. Without a
// version, such a change surfaces as an authentication failure indistinguishable
// from a corrupt row; with one, an unrecognised version is a specific error and
// a migration has something to branch on.
func TestTheEnvelopeIsVersionedAndRecognisable(t *testing.T) {
	sealer := crypto.NewSecretSealer([]byte("an install key"))
	sealed, err := sealer.Seal("JBSWY3DPEHPK3PXP")
	if err != nil {
		t.Fatalf("Seal: %v", err)
	}
	if !strings.HasPrefix(sealed, "v1.") {
		t.Errorf("sealed value is not versioned: %q", sealed)
	}
	if !crypto.IsSealed(sealed) {
		t.Error("IsSealed does not recognise its own output")
	}

	// A value written before sealing existed, or by a build with no key, is
	// distinguishable from a wrong key — so a caller can say which happened
	// instead of reporting a decryption failure for something never encrypted.
	if crypto.IsSealed("JBSWY3DPEHPK3PXP") {
		t.Error("a bare secret reads as sealed")
	}
	if _, err := sealer.Open("JBSWY3DPEHPK3PXP"); err == nil {
		t.Error("a bare secret opened")
	}
	if _, err := sealer.Open("v9.abcdef"); err == nil {
		t.Error("an unknown envelope version opened")
	}
}

// A nil sealer is what a Service built without one holds. It must refuse
// loudly: silently returning an empty secret would turn a configuration
// mistake into an account that stopped asking for its second factor.
func TestANilSealerRefusesRatherThanReturningNothing(t *testing.T) {
	var sealer *crypto.SecretSealer
	if _, err := sealer.Seal("x"); err == nil {
		t.Error("a nil sealer sealed something")
	}
	if _, err := sealer.Open("v1.abc"); err == nil {
		t.Error("a nil sealer opened something")
	}
}
