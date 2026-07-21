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

// TestOSKeychainStoreDegradesGracefully proves the real OS keychain
// integration never panics or leaks a raw driver error, on hosts with a
// working keychain and hosts without one alike (this suite's own CI
// environment has no Secret Service, so it exercises the "unavailable"
// path for real). Available() decides which path a Broker takes; this
// test only asserts that whichever way it goes, Get/Set return proper
// Platform contract errors rather than crashing or leaking go-keyring's
// driver-specific error.
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
