// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package crypto_test

import (
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// PasswordHasher must satisfy the domain.PasswordVerifier port the composition
// root wires it into. This assertion is the compile-time proof of that, and it
// lives in the external test package on purpose: it exercises the port without
// the production crypto package importing domain, so the adapter stays a pure,
// Platform-free crypto utility (see doc.go). Any type satisfying this port can
// be swapped in at main.go in its place.
var _ domain.PasswordVerifier = crypto.NewPasswordHasher()

func TestPasswordHashVerifyRoundTrip(t *testing.T) {
	h := crypto.NewPasswordHasher()
	const password = "correct horse battery staple"

	encoded, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	if !strings.HasPrefix(encoded, "$argon2id$v=19$") {
		t.Fatalf("encoding = %q, want a PHC argon2id string", encoded)
	}

	ok, err := h.Verify(password, encoded)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if !ok {
		t.Fatal("the right password did not verify")
	}

	wrong, err := h.Verify("wrong password", encoded)
	if err != nil {
		t.Fatalf("Verify wrong: %v", err)
	}
	if wrong {
		t.Fatal("the wrong password verified")
	}
}

// The salt is random, so the same password hashes differently each time and
// both encodings still verify.
func TestPasswordHashIsSalted(t *testing.T) {
	h := crypto.NewPasswordHasher()
	const password = "same password"

	a, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash a: %v", err)
	}
	b, err := h.Hash(password)
	if err != nil {
		t.Fatalf("Hash b: %v", err)
	}
	if a == b {
		t.Fatal("two hashes of one password are identical; the salt is not random")
	}
	for _, encoded := range []string{a, b} {
		ok, err := h.Verify(password, encoded)
		if err != nil || !ok {
			t.Fatalf("a salted hash failed to verify (ok=%v, err=%v)", ok, err)
		}
	}
}

func TestPasswordVerifyRejectsMalformed(t *testing.T) {
	h := crypto.NewPasswordHasher()
	for _, bad := range []string{
		"",
		"not-a-hash",
		"$argon2id$v=19$m=65536,t=3,p=4$onlyfivefields",
		"$bcrypt$v=19$m=1,t=1,p=1$c2FsdA$aGFzaA",
	} {
		if _, err := h.Verify("x", bad); err == nil {
			t.Fatalf("Verify(%q) returned no error; malformed input must be rejected", bad)
		}
	}
}
