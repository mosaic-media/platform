package runtime_test

import (
	"errors"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/runtime"
)

func TestMigrationTrackerLifecycle(t *testing.T) {
	tracker := runtime.NewMigrationTracker()
	if got := tracker.Status().Phase; got != runtime.MigrationRequired {
		t.Fatalf("initial Phase = %q, want %q", got, runtime.MigrationRequired)
	}

	tracker.Begin()
	if got := tracker.Status().Phase; got != runtime.MigrationRunning {
		t.Fatalf("Phase after Begin = %q, want %q", got, runtime.MigrationRunning)
	}

	tracker.Complete(nil)
	status := tracker.Status()
	if status.Phase != runtime.MigrationComplete {
		t.Fatalf("Phase after Complete(nil) = %q, want %q", status.Phase, runtime.MigrationComplete)
	}
	if status.Detail != "" {
		t.Fatalf("Detail after a successful migration = %q, want empty", status.Detail)
	}
}

func TestMigrationTrackerRecordsFailureDetail(t *testing.T) {
	tracker := runtime.NewMigrationTracker()
	tracker.Begin()
	tracker.Complete(errors.New("checksum mismatch for migration 0004"))

	status := tracker.Status()
	if status.Phase != runtime.MigrationFailed {
		t.Fatalf("Phase = %q, want %q", status.Phase, runtime.MigrationFailed)
	}
	if status.Detail != "checksum mismatch for migration 0004" {
		t.Fatalf("Detail = %q", status.Detail)
	}
}

func TestMigrationTrackerBeginClearsPriorDetail(t *testing.T) {
	tracker := runtime.NewMigrationTracker()
	tracker.Begin()
	tracker.Complete(errors.New("boom"))
	if tracker.Status().Detail == "" {
		t.Fatal("expected a detail after a failure")
	}

	tracker.Begin()
	if got := tracker.Status().Detail; got != "" {
		t.Fatalf("Detail after a fresh Begin = %q, want empty", got)
	}
}
