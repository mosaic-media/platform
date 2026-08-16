// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package crypto

import (
	"encoding/base64"
	"fmt"
	"strings"
)

// Sealing credential material that cannot be hashed (platform#79).
//
// A password is hashed; a TOTP secret cannot be, because a code is computed
// from the secret and so the Platform must read it back. That makes the row
// strictly more dangerous than password_credentials.hash: anyone who reads it
// mints valid codes for that account forever, and nothing about the account
// looks wrong afterwards.
//
// Encryption at rest does not defend against an attacker who has the running
// process, and does not claim to. It defends against the way this data actually
// escapes: a database backup, a replica, a dump pasted into an issue, a disk
// that left the building. Those carry the table and not the key.
//
// The accepted failure mode: lose the key and every enrolled user falls back to
// their recovery codes.

// sealedPrefix versions the envelope, so a later algorithm or key-derivation
// change surfaces as a specific, reportable error on an unrecognised version
// rather than as an authentication failure indistinguishable from a corrupt row.
const sealedPrefix = "v1."

// SecretSealer encrypts and decrypts values that must be recoverable.
//
// It holds the key rather than taking one per call: the only correct key here is
// the install's, and a per-call key is one a caller can get wrong.
type SecretSealer struct {
	key [32]byte
}

// NewSecretSealer builds a sealer over key material of any length. The material
// is run through DeriveKey, so a caller supplies whatever it holds rather than
// being required to produce exactly 32 bytes.
func NewSecretSealer(material []byte) *SecretSealer {
	return &SecretSealer{key: DeriveKey(material)}
}

// Seal encrypts plaintext into a storable string.
//
// AES-256-GCM, so the result is authenticated: a row somebody edited by hand
// fails to open rather than decrypting to something plausible. The nonce is
// random per call and travels with the ciphertext, so sealing the same secret
// twice produces different strings and a test must never assert on the exact
// output.
func (s *SecretSealer) Seal(plaintext string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("crypto: no sealer configured")
	}
	sealed, err := Encrypt(s.key, []byte(plaintext))
	if err != nil {
		return "", fmt.Errorf("crypto: seal: %w", err)
	}
	return sealedPrefix + base64.RawStdEncoding.EncodeToString(sealed), nil
}

// Open reverses Seal.
//
// Every failure is reported, never swallowed into an empty value. A secret that
// will not open is either a wrong key or a tampered row, and treating it as "no
// factor enrolled" would leave an account that quietly stopped asking for its
// second factor.
func (s *SecretSealer) Open(sealed string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("crypto: no sealer configured")
	}
	rest, ok := strings.CutPrefix(sealed, sealedPrefix)
	if !ok {
		return "", fmt.Errorf("crypto: sealed value is not %sencoded", sealedPrefix)
	}
	raw, err := base64.RawStdEncoding.DecodeString(rest)
	if err != nil {
		return "", fmt.Errorf("crypto: decode sealed value: %w", err)
	}
	plaintext, err := Decrypt(s.key, raw)
	if err != nil {
		// Deliberately not wrapped with the underlying message: AES-GCM's
		// authentication failure is the same error for a wrong key and a
		// tampered value, and repeating it adds nothing a reader can act on.
		return "", fmt.Errorf("crypto: sealed value did not open; the key is wrong or the value was altered")
	}
	return string(plaintext), nil
}

// IsSealed reports whether a stored value carries the envelope. It lets a caller
// tell a value written before sealing existed, or by a build with no key, from
// one that fails to open under a wrong key.
func IsSealed(value string) bool { return strings.HasPrefix(value, sealedPrefix) }
