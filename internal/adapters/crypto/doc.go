// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package crypto holds non-module-shaped cryptographic helpers: AES-256-GCM
// encryption and key derivation for the Secret Broker's encrypted local vault
// fallback (internal/platform/secrets), and an Argon2id PasswordHasher.
//
// These are adapters, not a built-in module: each helper fulfils a single small
// port rather than a broad contract surface, so there is no manifest and no
// registration through internal/composition/builtin — the composition root wires
// them directly. PasswordHasher satisfies the domain.PasswordVerifier port
// (Hash/Verify) and is passed to app.Service and the admin bootstrap in main.go,
// so swapping it for bcrypt, scrypt or an HSM-backed signer is a change there,
// behind the same port.
//
// The package imports no Platform code. The compile-time proof that
// PasswordHasher satisfies domain.PasswordVerifier therefore lives in the
// external test package (password_test.go), which asserts satisfaction of an
// external interface without coupling the adapter to it.
package crypto
