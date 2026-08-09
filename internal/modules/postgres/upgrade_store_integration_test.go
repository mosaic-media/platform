// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The upgrade request against a real database (ADR 0129).
func TestUpgradeRequestLifecycle(t *testing.T) {
	requirePostgres(t)
	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	store := postgres.NewUpgradeStore(pool)

	if _, err := store.Pending(ctx); contracts.CategoryOf(err) != contracts.NotFound {
		t.Fatalf("an empty register answered %v, want NotFound", err)
	}

	at := time.Now().UTC().Truncate(time.Millisecond)
	if err := store.Request(ctx, domain.UpgradeRequest{
		ID: "req-1", Version: "v0.4.0", RequestedBy: "user-1", RequestedAt: at,
	}); err != nil {
		t.Fatalf("Request: %v", err)
	}

	pending, err := store.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending: %v", err)
	}
	if pending.Version != "v0.4.0" || pending.RequestedBy != "user-1" {
		t.Fatalf("pending is %+v", pending)
	}

	// **A second press replaces the first rather than being refused**, and the
	// unique index is what would otherwise stop it: two pending requests would
	// be applied by one Supervisor in an order nobody chose.
	if err := store.Request(ctx, domain.UpgradeRequest{
		ID: "req-2", Version: "v0.5.0", RequestedAt: at,
	}); err != nil {
		t.Fatalf("replacing the request: %v", err)
	}
	pending, err = store.Pending(ctx)
	if err != nil {
		t.Fatalf("Pending after replacement: %v", err)
	}
	if pending.Version != "v0.5.0" {
		t.Fatalf("pending is %q, want the version most recently asked for", pending.Version)
	}

	if err := store.Settle(ctx); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	if _, err := store.Pending(ctx); contracts.CategoryOf(err) != contracts.NotFound {
		t.Fatalf("a settled request still reads as pending: %v", err)
	}
	// Settling nothing is success: settlement is driven by observing the
	// running version, and observing it twice must not be an error.
	if err := store.Settle(ctx); err != nil {
		t.Fatalf("settling nothing: %v", err)
	}
}
