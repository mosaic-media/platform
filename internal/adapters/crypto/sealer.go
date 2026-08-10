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
// **A password is hashed and a TOTP secret cannot be.** A code is computed from
// the secret, so the Platform must be able to read it back — which makes the
// row strictly more dangerous than `password_credentials.hash`: anyone who
// reads it mints valid codes for that account forever, and nothing about the
// account looks wrong afterwards.
//
// Encryption at rest does not defend against an attacker who has the running
// process, and it is not claimed to. What it defends against is the way this
// data actually escapes: a database backup, a replica, a dump pasted into an
// issue, a disk that left the building. Those carry the table and not the key.
//
// **The accepted failure mode is stated rather than discovered: lose the key
// and every enrolled user falls back to their recovery codes.** That is what
// recovery codes are for, and it is the trade taken deliberately — the
// alternative is a secret sitting in every backup in plaintext.

// sealedPrefix versions the envelope.
//
// **A format that cannot say what it is cannot be changed.** Without this, a
// later algorithm or key-derivation change would surface as an authentication
// failure that looks exactly like a corrupt row, and there would be no way to
// tell an old value from a broken one. With it, an unrecognised version is a
// specific, reportable error and a migration has something to branch on.
const sealedPrefix = "v1."

// SecretSealer encrypts and decrypts values that must be recoverable.
//
// It holds the key rather than taking one per call, because a caller that
// chooses a key per call is a caller that can choose the wrong one, and the
// only correct key here is the install's.
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
// random per call and travels with the ciphertext, which is why sealing the
// same secret twice produces different strings — and why a test must never
// assert on the exact output.
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
// **Every failure here is reported, never swallowed into an empty value.** A
// secret that will not open is either a wrong key or a tampered row, and both
// are situations an operator has to be told about — silently treating it as "no
// factor enrolled" would turn a key problem into an account that quietly stopped
// asking for its second factor, which is the worst way this could fail.
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

// IsSealed reports whether a stored value carries the envelope.
//
// It exists for the one case that will come up: a value written before sealing
// existed, or by a build that had no key. A caller can tell that apart from a
// wrong key and say so, rather than reporting a decryption failure for a value
// that was never encrypted.
func IsSealed(value string) bool { return strings.HasPrefix(value, sealedPrefix) }
