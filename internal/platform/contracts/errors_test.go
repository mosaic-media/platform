package contracts_test

import (
	"errors"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

func TestErrorCategoriesAreDistinct(t *testing.T) {
	categories := []contracts.ErrorCategory{
		contracts.InvalidArgument,
		contracts.Unauthenticated,
		contracts.PermissionDenied,
		contracts.NotFound,
		contracts.Conflict,
		contracts.Unavailable,
		contracts.Internal,
	}

	seen := make(map[contracts.ErrorCategory]bool, len(categories))
	for _, category := range categories {
		if seen[category] {
			t.Fatalf("duplicate error category: %s", category)
		}
		seen[category] = true
	}

	if len(seen) != 7 {
		t.Fatalf("expected 7 distinct error categories, got %d", len(seen))
	}
}

func TestErrorCategoryValuesAreStable(t *testing.T) {
	// Category string values are part of the contract's compatibility
	// surface (MEG-015 §03); a changed value here is a breaking change.
	tests := []struct {
		name     string
		category contracts.ErrorCategory
		want     string
	}{
		{"invalid argument", contracts.InvalidArgument, "invalid_argument"},
		{"unauthenticated", contracts.Unauthenticated, "unauthenticated"},
		{"permission denied", contracts.PermissionDenied, "permission_denied"},
		{"not found", contracts.NotFound, "not_found"},
		{"conflict", contracts.Conflict, "conflict"},
		{"unavailable", contracts.Unavailable, "unavailable"},
		{"internal", contracts.Internal, "internal"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if string(tt.category) != tt.want {
				t.Fatalf("category %s = %q, want %q", tt.name, tt.category, tt.want)
			}
		})
	}
}

func TestCategoryOfReturnsInternalForNonPlatformError(t *testing.T) {
	if got := contracts.CategoryOf(errors.New("boom")); got != contracts.Internal {
		t.Fatalf("CategoryOf() = %s, want %s", got, contracts.Internal)
	}
}

func TestCategoryOfReturnsCategoryFromPlatformError(t *testing.T) {
	err := contracts.NewError(contracts.NotFound, "user not found")

	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf() = %s, want %s", got, contracts.NotFound)
	}
}

func TestCategoryOfUnwrapsWrappedPlatformError(t *testing.T) {
	cause := errors.New("connection refused")
	err := contracts.WrapError(contracts.Unavailable, "reach store", cause)

	if got := contracts.CategoryOf(err); got != contracts.Unavailable {
		t.Fatalf("CategoryOf() = %s, want %s", got, contracts.Unavailable)
	}

	if !errors.Is(err, cause) {
		t.Fatal("expected errors.Is to find the wrapped cause")
	}

	var platformErr *contracts.Error
	if !errors.As(err, &platformErr) {
		t.Fatal("expected errors.As to find the platform error")
	}
	if platformErr.Category != contracts.Unavailable {
		t.Fatalf("platformErr.Category = %s, want %s", platformErr.Category, contracts.Unavailable)
	}
}

func TestContractMetadataIsStable(t *testing.T) {
	if contracts.ContractID != "mosaic.platform.contract" {
		t.Fatalf("ContractID = %q, want %q", contracts.ContractID, "mosaic.platform.contract")
	}
	if contracts.ContractVersion != "v1" {
		t.Fatalf("ContractVersion = %q, want %q", contracts.ContractVersion, "v1")
	}
}
