// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base32"
	"encoding/binary"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP, the second factor that works everywhere (platform#79).
//
// RFC 6238 over RFC 4226: the code is a truncation of HMAC(secret, counter),
// where the counter is the number of whole periods since the Unix epoch. There
// is no network, no origin and no certificate anywhere in it, which is the
// property that makes it usable on the LAN-only installs a passkey can never
// reach.
//
// SHA-1 is correct here rather than a lapse. RFC 6238 permits SHA-256 and
// SHA-512, and essentially no authenticator app implements them: Google
// Authenticator, Aegis and several password managers ignore the algorithm
// parameter entirely and assume SHA-1. A stronger digest would produce codes
// that silently disagree with whatever the user scanned the QR into, which fails
// as "wrong code" and is undiagnosable from either end. The construction is HMAC
// rather than a bare hash, so SHA-1's collision weakness is not relied on.
//
// This adapter is pure: it holds no state, reads no clock of its own and stores
// nothing — the caller supplies the instant and owns the secret. That is what
// lets the replay rule live in the application service where the last-used
// counter is stored, rather than in a component that would have to grow a
// database to enforce it.

// TOTPAuthenticator generates and verifies RFC 6238 codes.
//
// It satisfies the Platform's TOTP port structurally, so the crypto adapter
// stays free of any Platform import — the same arrangement PasswordHasher has
// with domain.PasswordVerifier.
type TOTPAuthenticator struct {
	digits int
	period time.Duration
	// skew is how many periods either side of the current one are accepted.
	skew int
}

// NewTOTPAuthenticator returns an authenticator with the parameters every
// authenticator app assumes: six digits over thirty seconds.
//
// One period of skew either side gives a 90-second window. Phone clocks drift,
// and a user typing a code that appears correct and is rejected has no way to
// tell drift from a wrong secret. Zero skew makes enrolment fail for a
// population whose clocks are slightly off; a wider window multiplies the
// guessing surface for no further benefit, since a human enters a code within
// seconds of reading it.
func NewTOTPAuthenticator() *TOTPAuthenticator {
	return &TOTPAuthenticator{digits: 6, period: 30 * time.Second, skew: 1}
}

// totpSecretBytes is the length of a generated secret.
//
// RFC 4226 requires at least 128 bits and recommends 160 — which is also the
// output size of the HMAC's digest, so a longer secret buys nothing against
// this construction. It encodes to exactly 32 base32 characters with no
// padding, which is what an authenticator app expects to be handed.
const totpSecretBytes = 20

// GenerateSecret returns a new shared secret, base32-encoded without padding.
//
// Base32 rather than base64 or hex because it is what otpauth:// URIs carry and
// what a person can retype from a screen without ambiguity — the alphabet has no
// 0/O or 1/l pair.
func (a *TOTPAuthenticator) GenerateSecret() (string, error) {
	buf := make([]byte, totpSecretBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto: read totp secret: %w", err)
	}
	return base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(buf), nil
}

// Code returns the code for secret at an instant. Exported because enrolment
// has to prove the round trip before it stores anything, and because a test
// that hard-codes expected digits is a test of its own arithmetic.
func (a *TOTPAuthenticator) Code(secret string, at time.Time) (string, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return "", err
	}
	return a.codeAt(key, a.step(at)), nil
}

// Verify reports whether code is valid for secret at an instant, and returns
// the counter step it matched.
//
// The step is returned so the caller can refuse a replay. A code is valid for
// the whole period it belongs to, so without recording the step that was spent
// the same six digits work again for up to a minute — long enough for a code
// read over a shoulder or relayed by a proxy to be used a second time. This
// adapter cannot enforce that itself because it stores nothing; the application
// service holds the last accepted step and requires the next to be greater.
//
// A malformed secret verifies nothing and is an error rather than a silent
// non-match: it means stored credential material is corrupt, which is a
// different problem from a user mistyping.
func (a *TOTPAuthenticator) Verify(secret, code string, at time.Time) (int64, bool, error) {
	key, err := decodeTOTPSecret(secret)
	if err != nil {
		return 0, false, err
	}
	presented := normaliseCode(code)
	if len(presented) != a.digits {
		return 0, false, nil
	}

	// Every candidate in the window is compared, without an early return, and
	// each comparison is constant-time. The digits are a low-entropy secret
	// being checked against user input, which is exactly the shape a timing
	// oracle preys on.
	current := a.step(at)
	var matched int64
	var found int
	for offset := -a.skew; offset <= a.skew; offset++ {
		step := current + int64(offset)
		equal := subtle.ConstantTimeCompare([]byte(a.codeAt(key, step)), []byte(presented))
		// Branch-free select: mask is all ones when equal, all zeros otherwise.
		mask := -int64(equal)
		matched = (matched &^ mask) | (step & mask)
		found |= equal
	}
	return matched, found == 1, nil
}

// ProvisioningURI renders the otpauth:// URI an authenticator app consumes,
// usually by scanning it as a QR code.
//
// The issuer appears twice — once as a label prefix and once as a parameter —
// which looks redundant and is not: older apps read the prefix and current ones
// read the parameter, and an app that reads neither files the account under a
// bare username with nothing saying which server it belongs to. Somebody with
// two self-hosted servers then has two indistinguishable entries.
//
// The parameters are stated explicitly rather than left to default, so a
// reading app cannot assume something other than what this code computes.
func (a *TOTPAuthenticator) ProvisioningURI(secret, issuer, account string) string {
	label := url.PathEscape(issuer + ":" + account)
	query := url.Values{}
	query.Set("secret", secret)
	query.Set("issuer", issuer)
	query.Set("algorithm", "SHA1")
	query.Set("digits", fmt.Sprint(a.digits))
	query.Set("period", fmt.Sprint(int(a.period.Seconds())))
	return "otpauth://totp/" + label + "?" + query.Encode()
}

// Period is how long one code lasts. The enrolment screen needs it to say so.
func (a *TOTPAuthenticator) Period() time.Duration { return a.period }

// step is the RFC 6238 counter: whole periods since the Unix epoch.
func (a *TOTPAuthenticator) step(at time.Time) int64 {
	return at.Unix() / int64(a.period.Seconds())
}

// codeAt is RFC 4226's HOTP with RFC 6238's time-derived counter.
func (a *TOTPAuthenticator) codeAt(key []byte, step int64) string {
	var counter [8]byte
	binary.BigEndian.PutUint64(counter[:], uint64(step))

	mac := hmac.New(sha1.New, key)
	mac.Write(counter[:])
	sum := mac.Sum(nil)

	// Dynamic truncation (RFC 4226 §5.3): the low nibble of the last byte picks
	// where in the digest to read four bytes from, and the top bit is cleared so
	// the result is positive on every platform's signed arithmetic.
	offset := sum[len(sum)-1] & 0x0f
	truncated := (uint32(sum[offset]&0x7f) << 24) |
		(uint32(sum[offset+1]) << 16) |
		(uint32(sum[offset+2]) << 8) |
		uint32(sum[offset+3])

	modulus := uint32(1)
	for range a.digits {
		modulus *= 10
	}
	return fmt.Sprintf("%0*d", a.digits, truncated%modulus)
}

// decodeTOTPSecret parses a base32 secret, tolerating how it is written down.
//
// A secret that has been through a screen and a clipboard arrives with spaces,
// in lower case, sometimes padded. All three are the same secret, and refusing
// them would fail as "wrong code" — the least diagnosable error this feature
// has.
func decodeTOTPSecret(secret string) ([]byte, error) {
	normalised := strings.ToUpper(strings.NewReplacer(" ", "", "-", "").Replace(secret))
	key, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(strings.TrimRight(normalised, "="))
	if err != nil {
		return nil, fmt.Errorf("crypto: decode totp secret: %w", err)
	}
	if len(key) == 0 {
		return nil, fmt.Errorf("crypto: totp secret is empty")
	}
	return key, nil
}

// normaliseCode strips what a person types around six digits.
func normaliseCode(code string) string {
	return strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code))
}

// recoveryCodeGroups and recoveryCodeGroupLen shape a recovery code as
// xxxxx-xxxxx: ten characters of the base32 alphabet, so 50 bits of entropy.
//
// Enough that guessing is not a threat, short enough to be written on paper and
// retyped correctly — which is the whole point of the artefact, since it is
// used exactly when the user has lost the device holding everything else.
const (
	recoveryCodeGroups   = 2
	recoveryCodeGroupLen = 5
)

// recoveryAlphabet excludes the letters and digits that are misread in
// handwriting. It is Crockford-style rather than RFC 4648's base32 for that
// reason: a code copied off paper has to survive a person's handwriting.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

// GenerateRecoveryCodes returns n single-use recovery codes.
//
// They are what stands between a lost phone and a lost account, and on a
// self-hosted server there is nobody to appeal to. The caller stores only their
// hashes (domain.RecoveryFactor holds a hash and never a code) and shows the
// plaintext exactly once.
func GenerateRecoveryCodes(n int) ([]string, error) {
	codes := make([]string, 0, n)
	for range n {
		code, err := generateRecoveryCode()
		if err != nil {
			return nil, err
		}
		codes = append(codes, code)
	}
	return codes, nil
}

func generateRecoveryCode() (string, error) {
	total := recoveryCodeGroups * recoveryCodeGroupLen
	buf := make([]byte, total)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("crypto: read recovery code: %w", err)
	}

	var out strings.Builder
	for i, b := range buf {
		if i > 0 && i%recoveryCodeGroupLen == 0 {
			out.WriteByte('-')
		}
		// The alphabet's length is not a power of two, so indexing a uniform
		// byte by it is very slightly biased: 256 mod 31 leaves eight values
		// marginally over-represented, costing a fraction of a bit against a
		// 50-bit code. Rejection sampling would remove it and is not worth the
		// loop.
		out.WriteByte(recoveryAlphabet[int(b)%len(recoveryAlphabet)])
	}
	return out.String(), nil
}

// NormaliseRecoveryCode puts a typed code into the form the hash was taken of,
// so case and the separator do not decide whether someone gets back into their
// account.
func NormaliseRecoveryCode(code string) string {
	return strings.ToLower(strings.NewReplacer(" ", "", "-", "").Replace(strings.TrimSpace(code)))
}
