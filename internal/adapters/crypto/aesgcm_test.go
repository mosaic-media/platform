// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package crypto_test

import (
	"bytes"
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	key := crypto.DeriveKey([]byte("a recovery key with plenty of entropy"))
	plaintext := []byte(`{"storage.postgres.password":"correct horse battery staple"}`)

	ciphertext, err := crypto.Encrypt(key, plaintext)
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if bytes.Contains(ciphertext, plaintext) {
		t.Fatal("ciphertext must not contain the plaintext verbatim")
	}

	got, err := crypto.Decrypt(key, ciphertext)
	if err != nil {
		t.Fatalf("Decrypt: %v", err)
	}
	if !bytes.Equal(got, plaintext) {
		t.Fatalf("Decrypt() = %q, want %q", got, plaintext)
	}
}

func TestDecryptWithWrongKeyFails(t *testing.T) {
	key := crypto.DeriveKey([]byte("recovery-key-one"))
	wrongKey := crypto.DeriveKey([]byte("recovery-key-two"))

	ciphertext, err := crypto.Encrypt(key, []byte("secret value"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	if _, err := crypto.Decrypt(wrongKey, ciphertext); err == nil {
		t.Fatal("expected an error decrypting with the wrong key")
	}
}

func TestDecryptTamperedCiphertextFails(t *testing.T) {
	key := crypto.DeriveKey([]byte("recovery-key"))
	ciphertext, err := crypto.Encrypt(key, []byte("secret value"))
	if err != nil {
		t.Fatalf("Encrypt: %v", err)
	}
	tampered := append([]byte(nil), ciphertext...)
	tampered[len(tampered)-1] ^= 0xFF

	if _, err := crypto.Decrypt(key, tampered); err == nil {
		t.Fatal("expected an error decrypting tampered ciphertext")
	}
}

func TestDecryptTooShortCiphertextFails(t *testing.T) {
	key := crypto.DeriveKey([]byte("recovery-key"))
	if _, err := crypto.Decrypt(key, []byte("short")); err == nil {
		t.Fatal("expected an error for ciphertext shorter than a nonce")
	}
}
