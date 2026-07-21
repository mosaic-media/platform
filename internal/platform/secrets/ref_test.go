// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package secrets_test

import (
	"testing"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/secrets"
)

func TestParseRefRoundTripsWithFormatRef(t *testing.T) {
	ref, err := secrets.ParseRef("secret://platform/postgres/password")
	if err != nil {
		t.Fatalf("ParseRef: %v", err)
	}
	if ref.Name != "platform/postgres/password" {
		t.Fatalf("ref.Name = %q, want %q", ref.Name, "platform/postgres/password")
	}
	if got := secrets.FormatRef(ref); got != "secret://platform/postgres/password" {
		t.Fatalf("FormatRef() = %q, want the original URI", got)
	}
}

func TestParseRefRejectsNonReferences(t *testing.T) {
	for _, in := range []string{"", "platform/postgres/password", "https://example.com", "secret:/missing-slash"} {
		if _, err := secrets.ParseRef(in); err == nil {
			t.Fatalf("ParseRef(%q): expected error, got nil", in)
		} else if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("ParseRef(%q): CategoryOf(err) = %s, want %s", in, got, contracts.InvalidArgument)
		}
	}
}

func TestParseRefRejectsEmptyName(t *testing.T) {
	if _, err := secrets.ParseRef("secret://"); err == nil {
		t.Fatal("expected an error for a reference with no name")
	}
}

func TestIsRefDistinguishesReferencesFromLiterals(t *testing.T) {
	if !secrets.IsRef("secret://platform/postgres/password") {
		t.Fatal("expected IsRef = true for a secret:// value")
	}
	if secrets.IsRef("hunter2") {
		t.Fatal("expected IsRef = false for a raw literal value")
	}
}

func TestFormatRefUnknownRefStillFormats(t *testing.T) {
	// FormatRef is a pure string builder: it does not validate that ref
	// resolves to anything, matching how config stores the reference
	// without knowing whether the secret it names exists yet.
	got := secrets.FormatRef(domain.SecretRef{Name: "platform/example/token"})
	if got != "secret://platform/example/token" {
		t.Fatalf("FormatRef() = %q", got)
	}
}
