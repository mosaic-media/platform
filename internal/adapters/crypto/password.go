package crypto

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// PasswordHasher hashes and verifies passwords with Argon2id — the production
// algorithm the domain's PasswordVerifier port was always shaped for
// (MEG-009 §03). It satisfies domain.PasswordVerifier structurally, so the
// crypto adapter stays free of any Platform import.
//
// A hash is encoded in the PHC string format
// ($argon2id$v=19$m=...,t=...,p=...$salt$hash), so the parameters travel with
// the hash and can be raised later without a migration: an old credential
// still verifies under the parameters recorded in its own string.
type PasswordHasher struct {
	time    uint32
	memory  uint32
	threads uint8
	keyLen  uint32
	saltLen uint32
}

// NewPasswordHasher returns a hasher with sensible interactive-login
// parameters: 64 MiB, three passes, a 16-byte salt and a 32-byte key.
func NewPasswordHasher() *PasswordHasher {
	return &PasswordHasher{
		time:    3,
		memory:  64 * 1024,
		threads: 4,
		keyLen:  32,
		saltLen: 16,
	}
}

// Hash derives an Argon2id hash of plaintext and returns it PHC-encoded.
func (h *PasswordHasher) Hash(plaintext string) (string, error) {
	salt := make([]byte, h.saltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("crypto: read salt: %w", err)
	}
	sum := argon2.IDKey([]byte(plaintext), salt, h.time, h.memory, h.threads, h.keyLen)

	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version,
		h.memory, h.time, h.threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(sum),
	), nil
}

// Verify reports whether plaintext hashes to the PHC-encoded value, using the
// parameters recorded in the encoding and a constant-time comparison. A
// malformed encoding is an error, not a silent non-match.
func (h *PasswordHasher) Verify(plaintext, encoded string) (bool, error) {
	params, salt, want, err := decodePHC(encoded)
	if err != nil {
		return false, err
	}
	got := argon2.IDKey([]byte(plaintext), salt, params.time, params.memory, params.threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1, nil
}

type argonParams struct {
	memory  uint32
	time    uint32
	threads uint8
}

// decodePHC parses a $argon2id$ PHC string into its parameters, salt and hash.
func decodePHC(encoded string) (argonParams, []byte, []byte, error) {
	parts := strings.Split(encoded, "$")
	// "" / "argon2id" / "v=19" / "m=..,t=..,p=.." / salt / hash
	if len(parts) != 6 || parts[1] != "argon2id" {
		return argonParams{}, nil, nil, errors.New("crypto: not an argon2id hash")
	}

	var version int
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("crypto: parse version: %w", err)
	}
	if version != argon2.Version {
		return argonParams{}, nil, nil, fmt.Errorf("crypto: unsupported argon2 version %d", version)
	}

	var p argonParams
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &p.memory, &p.time, &p.threads); err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("crypto: parse parameters: %w", err)
	}

	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("crypto: decode salt: %w", err)
	}
	hash, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return argonParams{}, nil, nil, fmt.Errorf("crypto: decode hash: %w", err)
	}
	return p, salt, hash, nil
}
