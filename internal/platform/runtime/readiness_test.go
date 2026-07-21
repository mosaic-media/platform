// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/diagnostics"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/runtime"
)

func TestCheckReadinessTrueWhenNothingUnavailable(t *testing.T) {
	registry := diagnostics.NewRegistry()
	registry.Register("postgres", fakeReporter{health: domain.ComponentHealth{Component: "postgres", Health: domain.HealthHealthy}})
	registry.Register("event-bus", fakeReporter{health: domain.ComponentHealth{Component: "event-bus", Health: domain.HealthDegraded}})

	result := runtime.CheckReadiness(context.Background(), registry)
	if !result.Ready {
		t.Fatal("expected Ready = true when no component is Unavailable, only Degraded")
	}
	if len(result.Components) != 2 {
		t.Fatalf("len(Components) = %d, want 2", len(result.Components))
	}
}

func TestCheckReadinessFalseWhenAnyComponentUnavailable(t *testing.T) {
	registry := diagnostics.NewRegistry()
	registry.Register("postgres", fakeReporter{health: domain.ComponentHealth{Component: "postgres", Health: domain.HealthUnavailable}})
	registry.Register("event-bus", fakeReporter{health: domain.ComponentHealth{Component: "event-bus", Health: domain.HealthHealthy}})

	result := runtime.CheckReadiness(context.Background(), registry)
	if result.Ready {
		t.Fatal("expected Ready = false when postgres is Unavailable")
	}
}

func TestCheckReadinessTrueWithNoRegisteredComponents(t *testing.T) {
	result := runtime.CheckReadiness(context.Background(), diagnostics.NewRegistry())
	if !result.Ready {
		t.Fatal("expected Ready = true with nothing registered to fail")
	}
}
