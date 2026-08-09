// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/runtime"
)

// fakeUpgrades is an in-memory UpgradeStore.
type fakeUpgrades struct {
	pending   *domain.UpgradeRequest
	settled   int
	settleErr error
}

func (f *fakeUpgrades) Request(_ context.Context, r domain.UpgradeRequest) error {
	f.pending = &r
	return nil
}

func (f *fakeUpgrades) Pending(context.Context) (domain.UpgradeRequest, error) {
	if f.pending == nil {
		return domain.UpgradeRequest{}, contracts.NewError(contracts.NotFound, "nothing requested")
	}
	return *f.pending, nil
}

func (f *fakeUpgrades) Settle(context.Context) error {
	f.settled++
	f.pending = nil
	return f.settleErr
}

func TestNothingPendingReportsNothing(t *testing.T) {
	got := runtime.CheckUpgrade(context.Background(), &fakeUpgrades{})
	if got.Pending {
		t.Fatalf("reported %+v with nothing requested", got)
	}
	// A build with no upgrade path answers the same way rather than failing.
	if runtime.CheckUpgrade(context.Background(), nil).Pending {
		t.Fatal("a nil store reported a pending request")
	}
}

func TestAPendingRequestIsReported(t *testing.T) {
	at := time.Date(2026, 8, 9, 12, 0, 0, 0, time.UTC)
	store := &fakeUpgrades{pending: &domain.UpgradeRequest{Version: "v0.4.0", RequestedAt: at}}

	got := runtime.CheckUpgrade(context.Background(), store)
	if !got.Pending || got.Version != "v0.4.0" || !got.RequestedAt.Equal(at) {
		t.Fatalf("reported %+v", got)
	}
	if store.settled != 0 {
		t.Fatal("a request was settled while the install is not running that version")
	}
}

// **Settlement is a comparison, not an acknowledgement** (ADR 0129). The
// process that would have reported success has been replaced by the upgrade, so
// what closes the request is the Platform being able to say it *is* the version
// that was asked for.
func TestARequestSettlesWhenTheInstallIsRunningThatVersion(t *testing.T) {
	t.Setenv("MOSAIC_GENERATION_ID", "v0.4.0")
	store := &fakeUpgrades{pending: &domain.UpgradeRequest{Version: "v0.4.0"}}

	got := runtime.CheckUpgrade(context.Background(), store)
	if got.Pending {
		t.Fatalf("still pending after the version landed: %+v", got)
	}
	if store.settled != 1 {
		t.Fatalf("settled %d times, want once", store.settled)
	}
}

// A deployment that runs the Platform itself sets no Generation id, and
// "unknown" must not read as "it is not that version" — nor as "it is". The
// request stays pending, which is honest: nothing there was going to carry it
// out either.
func TestAnUnknownGenerationSettlesNothing(t *testing.T) {
	t.Setenv("MOSAIC_GENERATION_ID", "")
	store := &fakeUpgrades{pending: &domain.UpgradeRequest{Version: "v0.4.0"}}

	got := runtime.CheckUpgrade(context.Background(), store)
	if !got.Pending {
		t.Fatal("a request was settled by a Platform that cannot say which Generation it is")
	}
	if store.settled != 0 {
		t.Fatalf("settled %d times, want none", store.settled)
	}
}

// A different Generation is not the one that was asked for, which is the case
// that matters after an activation reverts: the old Platform comes back, and
// its version does not match.
func TestADifferentGenerationLeavesTheRequestPending(t *testing.T) {
	t.Setenv("MOSAIC_GENERATION_ID", "v0.3.0")
	store := &fakeUpgrades{pending: &domain.UpgradeRequest{Version: "v0.4.0"}}

	if got := runtime.CheckUpgrade(context.Background(), store); !got.Pending {
		t.Fatalf("the request was settled by the wrong Generation: %+v", got)
	}
}
