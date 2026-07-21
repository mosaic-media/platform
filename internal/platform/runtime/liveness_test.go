// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime_test

import (
	"testing"

	"github.com/mosaic-media/platform/internal/platform/runtime"
)

func TestCheckLivenessAliveWhileStartingAndRunning(t *testing.T) {
	lifecycle := runtime.NewLifecycle()
	if got := runtime.CheckLiveness(lifecycle); !got.Alive {
		t.Fatalf("Alive = %v while Starting, want true", got.Alive)
	}

	lifecycle.MarkRunning()
	if got := runtime.CheckLiveness(lifecycle); !got.Alive {
		t.Fatalf("Alive = %v while Running, want true", got.Alive)
	}
}

func TestCheckLivenessNotAliveOnceStoppingOrStopped(t *testing.T) {
	lifecycle := runtime.NewLifecycle()
	lifecycle.MarkRunning()

	lifecycle.MarkStopping()
	if got := runtime.CheckLiveness(lifecycle); got.Alive {
		t.Fatal("expected Alive = false once shutdown has begun (MarkStopping)")
	}

	lifecycle.MarkStopped()
	if got := runtime.CheckLiveness(lifecycle); got.Alive {
		t.Fatal("expected Alive = false once Stopped")
	}
}
