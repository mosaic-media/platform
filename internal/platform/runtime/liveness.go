package runtime

import "github.com/mosaic-media/mosaic-platform/internal/platform/domain"

// LivenessResult is the Supervisor-facing answer to "should this process
// keep running" (MEG-015 §10 — Liveness health).
type LivenessResult struct {
	Alive     bool
	Lifecycle domain.LifecycleState
}

// CheckLiveness reports the process's own Lifecycle state. Unlike
// Readiness, it does not probe dependencies: a live-but-not-ready process
// (e.g. storage briefly unavailable) should not be killed and restarted by
// the Supervisor — only kept out of activation — since restarting would
// not fix an external dependency outage and would only cause churn.
// Liveness only goes false once this process has itself begun graceful
// shutdown (Lifecycle.MarkStopping/MarkStopped), telling the Supervisor
// this instance is intentionally going away, not crashed.
func CheckLiveness(lifecycle *Lifecycle) LivenessResult {
	state := lifecycle.State()
	alive := state == domain.LifecycleStarting || state == domain.LifecycleRunning
	return LivenessResult{Alive: alive, Lifecycle: state}
}
