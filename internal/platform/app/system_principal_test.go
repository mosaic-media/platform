// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The system principal is unbounded authority (ADR 0017), so what is worth
// testing is not that it works — a handler that let everything through would
// also pass that — but that nothing else can become it.

// A client controls its session reference, so the only thing standing between
// a client and unbounded authority is that the reference cannot be guessed or
// written down. These are the shapes somebody would try.
func TestAForgedSystemReferenceAuthenticatesAsNothing(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, testNow)

	for _, forged := range []string{
		"system",
		"mosaic.system",
		"system:",
		"system:0000000000000000000000000000000000000000000000000000000000000000",
		// The right shape and the wrong bytes, which is what a guess looks
		// like once somebody has read the source.
		"system:" + strings.Repeat("ab", 32),
	} {
		t.Run(forged, func(t *testing.T) {
			_, err := svc.PurgeTelemetry(context.Background(), app.PurgeTelemetryCommand{
				Caller: v1.Caller{Session: forged},
			})
			if err == nil {
				t.Fatal("a forged system reference was accepted")
			}
			if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
				t.Fatalf("CategoryOf(err) = %s, want %s (err = %v)", got, contracts.Unauthenticated, err)
			}
		})
	}
}

// The reference is minted per process, so one taken from a Service cannot be
// replayed against another. That is what makes it safe for it never to be
// persisted: there is nothing to leak between runs.
func TestTheSystemReferenceIsPerServiceAndNotReusable(t *testing.T) {
	first := newTestService(newFakeDB(), &trace{}, testNow)
	second := newTestService(newFakeDB(), &trace{}, testNow)

	if first.SystemCaller().Session == second.SystemCaller().Session {
		t.Fatal("two Services minted the same system reference")
	}
	if first.SystemCaller().Session == "" {
		t.Fatal("the system reference is empty, which would make every empty caller the system")
	}

	_, err := second.PurgeTelemetry(context.Background(), app.PurgeTelemetryCommand{
		Caller: first.SystemCaller(),
	})
	if err == nil {
		t.Fatal("one Service's system reference was accepted by another")
	}
	if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Unauthenticated)
	}
}

// The principal goes through the same boundary as anyone else — it is
// authenticated, then authorised — and the ordinary refusals are unchanged for
// everybody who is not it.
func TestTheSystemPrincipalPassesTheBoundaryAnOrdinaryCallerFails(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	maintenance := &fakeTelemetryMaintenance{}
	deps := baseTestDeps(db, tr, testNow)
	deps.TelemetryMaintenance = maintenance
	svc := app.NewService(deps)

	const session = "session-nobody"
	db.seedSession(session, "user-nobody", testNow)

	if _, err := svc.PurgeTelemetry(context.Background(), app.PurgeTelemetryCommand{
		Caller: v1.Caller{Session: session},
	}); contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("an ungranted caller got %v, want PermissionDenied", err)
	}
	if maintenance.swept != 0 {
		t.Fatal("a refused call still reached the store")
	}

	if _, err := svc.PurgeTelemetry(context.Background(), app.PurgeTelemetryCommand{
		Caller: svc.SystemCaller(),
	}); err != nil {
		t.Fatalf("the system principal was refused: %v", err)
	}
	if maintenance.swept != 1 {
		t.Fatalf("the sweep ran %d times, want 1", maintenance.swept)
	}
	// Extend before drop: the other order leaves a window at a day boundary in
	// which the partition being written to has been dropped and its
	// replacement does not exist.
	if !maintenance.extendedFirst {
		t.Fatal("partitions were dropped before the window was extended")
	}
}

// A Service built without a maintenance store must say so rather than report a
// sweep that did not happen — "dropped 0 partitions" and "there is nothing to
// drop from" are different facts.
func TestPurgeTelemetryWithoutAStoreIsUnavailableRatherThanSilent(t *testing.T) {
	svc := newTestService(newFakeDB(), &trace{}, testNow)
	_, err := svc.PurgeTelemetry(context.Background(), app.PurgeTelemetryCommand{
		Caller: svc.SystemCaller(),
	})
	if got := contracts.CategoryOf(err); got != contracts.Unavailable {
		t.Fatalf("CategoryOf(err) = %s, want %s (err = %v)", got, contracts.Unavailable, err)
	}
}

// fakeTelemetryMaintenance records that the sweep reached the store, and in
// which order.
type fakeTelemetryMaintenance struct {
	swept         int
	extended      int
	extendedFirst bool
}

func (f *fakeTelemetryMaintenance) EnsurePartitions(context.Context, time.Time, int) error {
	f.extended++
	return nil
}

func (f *fakeTelemetryMaintenance) DropExpiredPartitions(context.Context, time.Time, contracts.PartitionRetention) (int, error) {
	if f.extended > f.swept {
		f.extendedFirst = true
	}
	f.swept++
	return 0, nil
}

var _ contracts.TelemetryMaintenanceStore = (*fakeTelemetryMaintenance)(nil)
