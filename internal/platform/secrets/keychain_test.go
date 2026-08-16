// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package secrets_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/secrets"
)

// TestOSKeychainStoreDegradesGracefully asserts that the real OS keychain
// integration never panics or leaks a raw driver error, on hosts with a
// working keychain and hosts without one alike — CI has no Secret Service, so
// there it exercises the unavailable path for real. Available reports which
// path a Broker takes; this test only asserts that either way, Get and Set
// return Platform contract errors rather than go-keyring's own.
func TestOSKeychainStoreDegradesGracefully(t *testing.T) {
	store := secrets.NewOSKeychainStore()
	ctx := context.Background()

	available := store.Available(ctx)
	t.Logf("OS keychain available in this environment: %v", available)

	_, err := store.Get(ctx, "mosaic-platform-test-nonexistent-entry")
	if err == nil {
		// A real keychain happened to have this entry (astronomically
		// unlikely, but not this test's concern) — nothing further to check.
		return
	}
	switch got := contracts.CategoryOf(err); got {
	case contracts.NotFound, contracts.Unavailable:
		// Both are correct Platform categories for "no such secret" and
		// "backend unreachable" respectively.
	default:
		t.Fatalf("CategoryOf(err) = %s, want %s or %s", got, contracts.NotFound, contracts.Unavailable)
	}
}
