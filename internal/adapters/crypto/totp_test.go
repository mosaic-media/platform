// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package crypto_test

import (
	"encoding/base32"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/adapters/crypto"
)

// rfcSecret is RFC 6238's own SHA-1 test key, encoded rather than transcribed.
//
// The RFC states the key as the ASCII string; writing its base32 form by hand
// would make this a test of my transcription. Deriving it means a wrong
// encoding fails against the vectors below rather than passing quietly.
func rfcSecret() string {
	return base32.StdEncoding.WithPadding(base32.NoPadding).
		EncodeToString([]byte("12345678901234567890"))
}

// **The most valuable test in this file: RFC 6238's published vectors.**
//
// An authenticator app and this code have to agree on a number neither ever
// sends to the other, so the only thing that can hold them together is the
// specification. A test that asserted "the code this code produced" would pass
// against any arithmetic at all, including arithmetic no phone agrees with —
// and the failure would surface as a user's correct code being rejected, which
// is undiagnosable from either end.
//
// The published vectors are eight digits; Mosaic issues six. The six-digit code
// is the last six of the eight, because both are the same truncated value taken
// modulo a different power of ten.
func TestCodesMatchRFC6238Vectors(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	secret := rfcSecret()

	for _, tc := range []struct {
		unix  int64
		eight string
	}{
		{59, "94287082"},
		{1111111109, "07081804"},
		{1111111111, "14050471"},
		{1234567890, "89005924"},
		{2000000000, "69279037"},
		{20000000000, "65353130"},
	} {
		want := tc.eight[len(tc.eight)-6:]
		got, err := auth.Code(secret, time.Unix(tc.unix, 0).UTC())
		if err != nil {
			t.Fatalf("T=%d: %v", tc.unix, err)
		}
		if got != want {
			t.Errorf("T=%d: code is %q, want %q (from RFC vector %s)", tc.unix, got, want, tc.eight)
		}
	}
}

// The code a user reads is the code this accepts — the round trip a phone makes
// every thirty seconds.
func TestVerifyAcceptsTheCurrentCode(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	secret, err := auth.GenerateSecret()
	if err != nil {
		t.Fatalf("GenerateSecret: %v", err)
	}
	now := time.Unix(1_700_000_000, 0).UTC()

	code, err := auth.Code(secret, now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	step, ok, err := auth.Verify(secret, code, now)
	if err != nil || !ok {
		t.Fatalf("Verify(current) = %v, %v, %v; want a match", step, ok, err)
	}
	if step != now.Unix()/30 {
		t.Errorf("matched step %d, want %d", step, now.Unix()/30)
	}
}

// **Skew is what makes this usable by people whose phone clock is a little
// off**, and its bounds are a real decision: one period either side, not two.
// Too narrow rejects correct codes for a population that cannot tell drift from
// a wrong secret; too wide multiplies the guessing surface for no gain.
func TestVerifyAcceptsOnePeriodOfDriftAndNoMore(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	secret := rfcSecret()
	now := time.Unix(1_700_000_040, 0).UTC()

	for _, drift := range []time.Duration{-30 * time.Second, 0, 30 * time.Second} {
		code, err := auth.Code(secret, now.Add(drift))
		if err != nil {
			t.Fatalf("Code(%v): %v", drift, err)
		}
		if _, ok, err := auth.Verify(secret, code, now); err != nil || !ok {
			t.Errorf("a code %v out was rejected; drift within one period must be accepted", drift)
		}
	}
	for _, drift := range []time.Duration{-90 * time.Second, 90 * time.Second} {
		code, err := auth.Code(secret, now.Add(drift))
		if err != nil {
			t.Fatalf("Code(%v): %v", drift, err)
		}
		if _, ok, _ := auth.Verify(secret, code, now); ok {
			t.Errorf("a code %v out was accepted; the window must not extend that far", drift)
		}
	}
}

// **The returned step is what makes replay preventable.** A code stays valid
// for its whole period, so without recording which step was spent the same six
// digits work again for up to a minute — long enough for a code read over a
// shoulder, or relayed by a proxy, to be used twice. This adapter stores
// nothing, so it hands the caller the number to compare against.
func TestVerifyReturnsTheStepSoAReplayCanBeRefused(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	secret := rfcSecret()
	now := time.Unix(1_700_000_000, 0).UTC()

	code, err := auth.Code(secret, now)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	first, ok, err := auth.Verify(secret, code, now)
	if err != nil || !ok {
		t.Fatalf("first use rejected: %v %v", ok, err)
	}
	// The same code, a moment later and still inside its period, matches the
	// same step — which is exactly what a caller compares to refuse it.
	second, ok, err := auth.Verify(secret, code, now.Add(5*time.Second))
	if err != nil || !ok {
		t.Fatalf("second use rejected by the adapter: %v %v", ok, err)
	}
	if first != second {
		t.Errorf("the same code matched steps %d then %d; a replay would be indistinguishable from a new code",
			first, second)
	}
	// And a later period is a strictly greater step, so "greater than last
	// used" is a usable rule rather than an accident of this instant.
	later, ok, err := auth.Verify(secret, mustCode(t, auth, secret, now.Add(60*time.Second)), now.Add(60*time.Second))
	if err != nil || !ok {
		t.Fatalf("a later code was rejected: %v %v", ok, err)
	}
	if later <= first {
		t.Errorf("a later period produced step %d, not greater than %d", later, first)
	}
}

// A secret that has been through a screen and a clipboard arrives lower-cased,
// spaced or padded. All three are the same secret, and refusing them would
// surface as "wrong code" — the least diagnosable failure this feature has.
func TestSecretsAreToleratedAsPeopleWriteThem(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	canonical := rfcSecret()
	now := time.Unix(1_700_000_000, 0).UTC()
	want := mustCode(t, auth, canonical, now)

	for _, written := range []string{
		strings.ToLower(canonical),
		spaceEveryFour(canonical),
		canonical + "======",
	} {
		got, err := auth.Code(written, now)
		if err != nil {
			t.Errorf("Code(%q): %v", written, err)
			continue
		}
		if got != want {
			t.Errorf("secret written as %q produced %q, want %q", written, got, want)
		}
	}
}

// Corrupt stored credential material is an error rather than a silent
// non-match: "the secret in the database is not decodable" and "the user
// mistyped" are different problems and must not look the same.
func TestAMalformedSecretIsAnErrorNotANonMatch(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	for _, bad := range []string{"", "!!!!not base32!!!!", "0189"} {
		if _, _, err := auth.Verify(bad, "123456", time.Now()); err == nil {
			t.Errorf("Verify with secret %q returned no error", bad)
		}
	}
}

// A code of the wrong length is a user mistake, so it is a plain non-match with
// no error — the opposite of the case above, and worth pinning beside it.
func TestAWrongLengthCodeIsAPlainNonMatch(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	secret := rfcSecret()
	for _, code := range []string{"", "1234", "1234567", "abcdef"} {
		step, ok, err := auth.Verify(secret, code, time.Now())
		if err != nil {
			t.Errorf("Verify(%q) errored: %v", code, err)
		}
		if ok || step != 0 {
			t.Errorf("Verify(%q) = %d, %v; want no match", code, step, ok)
		}
	}
}

// A generated secret has to be usable, unpredictable, and the length an
// authenticator app expects to be handed.
func TestGeneratedSecretsAreUsableAndDistinct(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	seen := map[string]bool{}
	for range 16 {
		secret, err := auth.GenerateSecret()
		if err != nil {
			t.Fatalf("GenerateSecret: %v", err)
		}
		if len(secret) != 32 {
			t.Errorf("secret is %d characters, want 32 (160 bits, RFC 4226's recommendation)", len(secret))
		}
		if strings.Contains(secret, "=") {
			t.Errorf("secret carries base32 padding: %q", secret)
		}
		if seen[secret] {
			t.Fatalf("GenerateSecret repeated a secret: %q", secret)
		}
		seen[secret] = true

		if _, err := auth.Code(secret, time.Now()); err != nil {
			t.Errorf("a generated secret does not produce a code: %v", err)
		}
	}
}

// The provisioning URI is the whole of what an app learns at enrolment. Every
// parameter is stated rather than defaulted, so a reading app cannot assume
// something other than what this code computes.
func TestProvisioningURIStatesEveryParameter(t *testing.T) {
	auth := crypto.NewTOTPAuthenticator()
	raw := auth.ProvisioningURI("JBSWY3DPEHPK3PXP", "Mosaic", "ada@example.com")

	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("the URI does not parse: %v (%q)", err, raw)
	}
	if parsed.Scheme != "otpauth" || parsed.Host != "totp" {
		t.Errorf("scheme/host is %q/%q, want otpauth/totp", parsed.Scheme, parsed.Host)
	}
	query := parsed.Query()
	for key, want := range map[string]string{
		"secret":    "JBSWY3DPEHPK3PXP",
		"issuer":    "Mosaic",
		"algorithm": "SHA1",
		"digits":    "6",
		"period":    "30",
	} {
		if got := query.Get(key); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
	// The label carries the issuer too. It looks redundant beside the
	// parameter and is not: apps differ in which they read, and one that reads
	// neither files the account under a bare username with nothing saying
	// which server it belongs to.
	if !strings.Contains(parsed.Path, "Mosaic") || !strings.Contains(parsed.Path, "ada@example.com") {
		t.Errorf("label is %q, want it to carry both issuer and account", parsed.Path)
	}
}

// Recovery codes are used exactly when the user has lost the device holding
// everything else, so they are read off paper and retyped. The shape is the
// feature.
func TestRecoveryCodesAreDistinctAndRetypeable(t *testing.T) {
	codes, err := crypto.GenerateRecoveryCodes(10)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	if len(codes) != 10 {
		t.Fatalf("got %d codes, want 10", len(codes))
	}

	seen := map[string]bool{}
	for _, code := range codes {
		if seen[code] {
			t.Fatalf("a recovery code repeated within one set: %q", code)
		}
		seen[code] = true

		if len(code) != 11 || strings.Count(code, "-") != 1 {
			t.Errorf("code %q is not the xxxxx-xxxxx shape", code)
		}
		// The alphabet excludes what is misread in handwriting. A code
		// containing `0`, `o`, `1` or `l` is one somebody will retype wrongly
		// on the worst day they have with this software.
		for _, bad := range []string{"0", "o", "1", "l", "i"} {
			if strings.Contains(code, bad) {
				t.Errorf("code %q contains %q, which is misread from paper", code, bad)
			}
		}
	}
}

// However somebody retypes it, it has to hash to the same thing that was
// stored, or a recovery code is a way to lose an account rather than to keep
// one.
func TestRecoveryCodeNormalisationSurvivesRetyping(t *testing.T) {
	codes, err := crypto.GenerateRecoveryCodes(1)
	if err != nil {
		t.Fatalf("GenerateRecoveryCodes: %v", err)
	}
	canonical := crypto.NormaliseRecoveryCode(codes[0])

	for _, typed := range []string{
		codes[0],
		strings.ToUpper(codes[0]),
		strings.ReplaceAll(codes[0], "-", " "),
		"  " + codes[0] + "  ",
		strings.ReplaceAll(codes[0], "-", ""),
	} {
		if got := crypto.NormaliseRecoveryCode(typed); got != canonical {
			t.Errorf("%q normalised to %q, want %q", typed, got, canonical)
		}
	}
}

func mustCode(t *testing.T, auth *crypto.TOTPAuthenticator, secret string, at time.Time) string {
	t.Helper()
	code, err := auth.Code(secret, at)
	if err != nil {
		t.Fatalf("Code: %v", err)
	}
	return code
}

func spaceEveryFour(s string) string {
	var out strings.Builder
	for i, r := range s {
		if i > 0 && i%4 == 0 {
			out.WriteByte(' ')
		}
		out.WriteRune(r)
	}
	return out.String()
}
