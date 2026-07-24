// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"testing"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The layer-3 posture is decided from the platform and the deployment's
// attestation (ADR 0064, ADR 0080). These pin the honest cases: enforcement is
// only ever reported where the OS can deliver it AND the deployment says it did,
// and a claim on a platform that has no mechanism is downgraded rather than
// believed.
func TestDetermineEgressContainment(t *testing.T) {
	cases := []struct {
		name         string
		goos         string
		declared     string
		wantEnforced bool
	}{
		{"linux, declared enforced → enforced", "linux", "enforced", true},
		{"linux, not declared → attributed", "linux", "", false},
		{"linux, garbage declaration → attributed (a typo must not assert)", "linux", "yes", false},
		{"darwin, declared enforced → still attributed (no mechanism)", "darwin", "enforced", false},
		{"darwin, not declared → attributed", "darwin", "", false},
		{"windows, declared enforced → still attributed", "windows", "enforced", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extension.DetermineEgressContainment(tc.goos, tc.declared)
			if got.Enforced != tc.wantEnforced {
				t.Errorf("Enforced = %v, want %v (%s)", got.Enforced, tc.wantEnforced, got.Detail)
			}
			if got.Detail == "" {
				t.Error("every posture must carry an honest detail sentence")
			}
			wantSummary := "attributed but not enforced"
			if tc.wantEnforced {
				wantSummary = "enforced"
			}
			if got.Summary() != wantSummary {
				t.Errorf("Summary() = %q, want %q", got.Summary(), wantSummary)
			}
		})
	}
}

// The default — nothing declared — is never "enforced". This is the property that
// keeps the layer-3 claim from being made where a deployment did not attest it.
func TestEgressContainmentDefaultsToNotEnforced(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows", "freebsd"} {
		if extension.DetermineEgressContainment(goos, "").Enforced {
			t.Errorf("%s with no declaration should not report enforced", goos)
		}
	}
}
