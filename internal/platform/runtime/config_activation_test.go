package runtime_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/runtime"
)

func TestCheckConfigActivationNoActiveVersion(t *testing.T) {
	status := runtime.CheckConfigActivation(context.Background(), &fakeConfigStore{})
	if status.HasActiveVersion {
		t.Fatal("expected HasActiveVersion = false with nothing activated")
	}
}

func TestCheckConfigActivationReturnsActiveVersionAndHotReloadClass(t *testing.T) {
	now := time.Date(2026, 8, 14, 12, 0, 0, 0, time.UTC)
	store := &fakeConfigStore{}
	store.setActive(domain.ConfigVersion{
		ID:          "cv-1",
		Status:      domain.ConfigActive,
		CreatedAt:   now.Add(-time.Hour),
		ActivatedAt: &now,
	})

	status := runtime.CheckConfigActivation(context.Background(), store)
	if !status.HasActiveVersion {
		t.Fatal("expected HasActiveVersion = true")
	}
	if status.VersionID != "cv-1" {
		t.Fatalf("VersionID = %q, want cv-1", status.VersionID)
	}
	if !status.ActivatedAt.Equal(now) {
		t.Fatalf("ActivatedAt = %v, want %v", status.ActivatedAt, now)
	}
	// Only a Hot-classified change can ever reach Active (config.Manager.
	// Activate defers anything else) — this is a structural invariant, not
	// a value CheckConfigActivation guesses at.
	if status.ReloadClass != string(config.Hot) {
		t.Fatalf("ReloadClass = %q, want %q", status.ReloadClass, config.Hot)
	}
}
