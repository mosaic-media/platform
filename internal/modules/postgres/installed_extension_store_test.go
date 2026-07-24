// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The installed-extensions store is the durable record the Platform reads at
// boot to re-adopt what a user installed (ADR 0081). This exercises the whole of
// it against real PostgreSQL: an empty set is the honest default, an install is
// recorded and read back, a reinstall replaces rather than duplicates, and an
// uninstall is idempotent.
func TestInstalledExtensionStoreRoundTrip(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	ctx := context.Background()
	if err := postgres.Migrate(ctx, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)
	store := cs.InstalledExtensions

	// Default-empty: a fresh Platform has installed nothing, and that is not an
	// error — it is the state boot re-adoption expects to find most of the time.
	got, err := store.List(ctx)
	if err != nil {
		t.Fatalf("List on an empty store: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("a fresh store should be empty, got %d", len(got))
	}

	// A user installs the Stremio addon source — the shape a real install record
	// takes (ADR 0081): the module id the binary reports, the repository it came
	// from, the version pinned, and who verified it.
	when := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	rec := domain.InstalledExtension{
		ModuleID:    "stremio",
		Repository:  "mosaic-official",
		Version:     "v0.24.0",
		SignedBy:    "mosaic-official",
		InstalledAt: when,
	}
	if _, err := store.Upsert(ctx, rec); err != nil {
		t.Fatalf("Upsert: %v", err)
	}

	got, err = store.List(ctx)
	if err != nil {
		t.Fatalf("List after install: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("after one install, got %d records", len(got))
	}
	// Compare field by field, with Equal for the instant: a timestamptz round-trips
	// to the same instant but a struct-equal `==` would fail on the location
	// pointer the driver hands back.
	if g := got[0]; g.ModuleID != rec.ModuleID || g.Repository != rec.Repository ||
		g.Version != rec.Version || g.SignedBy != rec.SignedBy || !g.InstalledAt.Equal(rec.InstalledAt) {
		t.Errorf("record did not round-trip: got %+v, want %+v", got[0], rec)
	}

	// A reinstall at a new version replaces the row rather than adding a second —
	// the module id is the key, so an install list never carries a module twice.
	upgraded := rec
	upgraded.Version = "v0.25.0"
	upgraded.InstalledAt = when.Add(time.Hour)
	if _, err := store.Upsert(ctx, upgraded); err != nil {
		t.Fatalf("Upsert (reinstall): %v", err)
	}
	got, err = store.List(ctx)
	if err != nil {
		t.Fatalf("List after reinstall: %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("a reinstall must replace, not duplicate: got %d records", len(got))
	}
	if got[0].Version != "v0.25.0" {
		t.Errorf("reinstall did not update the version: got %q", got[0].Version)
	}

	// Uninstall removes it, and uninstalling again is not an error — the contract
	// promises idempotence so a retried uninstall does not fail.
	if err := store.Remove(ctx, "stremio"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	if err := store.Remove(ctx, "stremio"); err != nil {
		t.Fatalf("Remove of an absent module should be idempotent, got: %v", err)
	}
	got, err = store.List(ctx)
	if err != nil {
		t.Fatalf("List after uninstall: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("after uninstall the store should be empty, got %d", len(got))
	}
}
