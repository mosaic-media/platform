// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime_test

import (
	"testing"

	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/runtime"
)

func TestLifecycleTransitions(t *testing.T) {
	lifecycle := runtime.NewLifecycle()
	if got := lifecycle.State(); got != domain.LifecycleStarting {
		t.Fatalf("initial State() = %q, want %q", got, domain.LifecycleStarting)
	}

	lifecycle.MarkRunning()
	if got := lifecycle.State(); got != domain.LifecycleRunning {
		t.Fatalf("State() after MarkRunning = %q, want %q", got, domain.LifecycleRunning)
	}

	lifecycle.MarkStopping()
	if got := lifecycle.State(); got != domain.LifecycleStopping {
		t.Fatalf("State() after MarkStopping = %q, want %q", got, domain.LifecycleStopping)
	}

	lifecycle.MarkStopped()
	if got := lifecycle.State(); got != domain.LifecycleStopped {
		t.Fatalf("State() after MarkStopped = %q, want %q", got, domain.LifecycleStopped)
	}
}
