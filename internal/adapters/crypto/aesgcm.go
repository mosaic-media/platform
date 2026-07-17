package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"errors"
	"io"
)

// DeriveKey derives a 32-byte AES-256 key from arbitrary-length key
// material via SHA-256. This is a first-cut KDF: it has no configurable
// work factor, so it is appropriate for a recovery key that is itself
// high-entropy random material (MEG-015 §08's vault recovery key), not for
// deriving a key from a low-entropy human password. A stronger, tunable
// KDF (Argon2id/scrypt) can replace this later without changing the
// Encrypt/Decrypt contract.
func DeriveKey(material []byte) [32]byte {
	return sha256.Sum256(material)
}

// Encrypt encrypts plaintext with AES-256-GCM under key, returning
// nonce||ciphertext.
func Encrypt(key [32]byte, plaintext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return nil, err
	}
	return gcm.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt reverses Encrypt. It fails if ciphertext was not produced by
// Encrypt under the same key, including a wrong key (authentication
// failure, not a decodable-but-wrong plaintext).
func Decrypt(key [32]byte, ciphertext []byte) ([]byte, error) {
	gcm, err := newGCM(key)
	if err != nil {
		return nil, err
	}
	if len(ciphertext) < gcm.NonceSize() {
		return nil, errors.New("crypto: ciphertext too short")
	}
	nonce, sealed := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	return gcm.Open(nil, nonce, sealed, nil)
}

func newGCM(key [32]byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}
