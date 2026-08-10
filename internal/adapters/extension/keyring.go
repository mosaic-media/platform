// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"crypto/ed25519"
	"fmt"
)

// Keyring is the set of publisher keys the Platform trusts (platform#40). Signing
// is universal — every artefact is signed — so trust is not about *whether*
// something is signed but about *whose* key signed it. Mosaic's own key is
// trusted by default; a user-added repository adds its key with explicit consent
// (platform#49 puts that consent surface in the Platform).
//
// ed25519 is the signature scheme: small keys, small signatures, fast
// verification, and in the standard library, so no dependency rides on a
// security-critical path. It is what a minisign-style signing tool produces.
type Keyring struct {
	keys []trustedKey
}

type trustedKey struct {
	// id names the publisher a key belongs to, so a verified signature can be
	// reported as provenance — "signed by mosaic-official" — rather than a bare
	// yes. platform#40 requires provenance to stay visible after install, and a
	// verified module's signer is where that begins.
	id  string
	pub ed25519.PublicKey
}

// NewKeyring returns an empty keyring. A serving Platform seeds it with the
// official key; a test seeds it with a test key.
func NewKeyring() *Keyring { return &Keyring{} }

// Trust adds a publisher key. An id that is already present is replaced, so
// rotating a publisher's key is adding it again rather than a distinct
// operation.
func (k *Keyring) Trust(id string, pub ed25519.PublicKey) error {
	if id == "" {
		return fmt.Errorf("extension: a trusted key needs a publisher id")
	}
	if len(pub) != ed25519.PublicKeySize {
		return fmt.Errorf("extension: %q is not an ed25519 public key (got %d bytes, want %d)", id, len(pub), ed25519.PublicKeySize)
	}
	for i := range k.keys {
		if k.keys[i].id == id {
			k.keys[i].pub = pub
			return nil
		}
	}
	k.keys = append(k.keys, trustedKey{id: id, pub: pub})
	return nil
}

// verify reports whether any trusted key signed message, and which. Trying every
// key rather than requiring the signature to name one keeps the signature format
// minimal — a bare ed25519 signature — and the cost is negligible at the handful
// of keys a keyring holds. A verified signature's key id is returned as
// provenance; an unverifiable one returns "" and false.
func (k *Keyring) verify(message, sig []byte) (keyID string, ok bool) {
	if len(sig) != ed25519.SignatureSize {
		return "", false
	}
	for _, tk := range k.keys {
		if ed25519.Verify(tk.pub, message, sig) {
			return tk.id, true
		}
	}
	return "", false
}

// empty reports whether the keyring trusts no keys. A verification against an
// empty keyring can only fail, and failing with "no trusted keys" is more useful
// than "signature did not verify".
func (k *Keyring) empty() bool { return len(k.keys) == 0 }
