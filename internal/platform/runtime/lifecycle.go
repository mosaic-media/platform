// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime

import (
	"sync"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// Lifecycle tracks this process's own position in its startup/shutdown
// lifecycle, reusing domain.LifecycleState so the
// Platform's own state uses the same vocabulary every other reported
// component already does.
type Lifecycle struct {
	mu    sync.Mutex
	state domain.LifecycleState
}

// NewLifecycle returns a Lifecycle starting in LifecycleStarting.
func NewLifecycle() *Lifecycle {
	return &Lifecycle{state: domain.LifecycleStarting}
}

// MarkRunning records that startup has completed and the process is
// serving.
func (l *Lifecycle) MarkRunning() { l.set(domain.LifecycleRunning) }

// MarkStopping records that graceful shutdown has begun.
func (l *Lifecycle) MarkStopping() { l.set(domain.LifecycleStopping) }

// MarkStopped records that shutdown has completed.
func (l *Lifecycle) MarkStopped() { l.set(domain.LifecycleStopped) }

func (l *Lifecycle) set(state domain.LifecycleState) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.state = state
}

// State returns the current lifecycle state.
func (l *Lifecycle) State() domain.LifecycleState {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.state
}
