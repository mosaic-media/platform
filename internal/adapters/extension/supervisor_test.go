// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The lifecycle tests (platform#39 step 3). They run a module that actually dies —
// extprobe self-destructs on command — and assert the Platform's response:
// restart with backoff, recovery, crash-loop disable, and a degraded capability
// rather than a Platform crash while the module is down.
//
// The policy here is deliberately fast so the suite stays quick; the shape it
// exercises is the production one, only with the clocks turned up.

func fastPolicy() extension.RestartPolicy {
	return extension.RestartPolicy{
		InitialBackoff:         20 * time.Millisecond,
		MaxBackoff:             80 * time.Millisecond,
		HealthyThreshold:       150 * time.Millisecond,
		MaxConsecutiveFailures: 3,
		ProbeInterval:          20 * time.Millisecond,
		ProbeTimeout:           500 * time.Millisecond,
	}
}

func supervise(t *testing.T, env []string, policy extension.RestartPolicy) (*extension.Supervised, *recordingTelemetry) {
	t.Helper()
	tel := &recordingTelemetry{}
	s, err := extension.Supervise(extension.Config{
		BinaryPath: buildProbe(t),
		Content:    &recordingContent{},
		Env:        env,
	}, policy, tel)
	if err != nil {
		t.Fatalf("supervise: %v", err)
	}
	t.Cleanup(s.Close)
	return s, tel
}

// waitForState polls until the supervised module reaches want, or fails.
func waitForState(t *testing.T, s *extension.Supervised, want extension.State, within time.Duration) {
	t.Helper()
	deadline := time.Now().Add(within)
	for time.Now().Before(deadline) {
		if s.State() == want {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("module did not reach %s within %v (last: %s)", want, within, s.State())
}

// A module that dies once is restarted and recovers. The marker file makes the
// first process die and the second survive, which is a transient crash.
func TestModuleRecoversFromATransientCrash(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "crashed")
	s, _ := supervise(t, []string{
		"EXTPROBE_EXIT_AFTER_MS=60",
		"EXTPROBE_CRASH_ONCE=" + marker,
	}, fastPolicy())

	// It starts running, dies, and comes back running.
	waitForState(t, s, extension.StateRestarting, 2*time.Second)
	waitForState(t, s, extension.StateRunning, 2*time.Second)

	// And once back, it answers — Supervised is the proxy the registry holds, and
	// it routes to the new process without the registry knowing anything changed.
	waitForState(t, s, extension.StateRunning, time.Second)
	out, err := s.Search(context.Background(), v1.SearchRequest{
		Caller: v1.CallerFromSession("h"), Text: "x",
	})
	if err != nil {
		t.Fatalf("search after recovery: %v", err)
	}
	if len(out.Results) != 1 {
		t.Errorf("recovered module returned %d results, want 1", len(out.Results))
	}
}

// A module that will not stay up is disabled after the policy's limit rather
// than restarted forever — and disabling it is never a Platform exit.
func TestModuleThatCrashLoopsIsDisabled(t *testing.T) {
	s, tel := supervise(t, []string{"EXTPROBE_EXIT_AFTER_MS=30"}, fastPolicy())

	waitForState(t, s, extension.StateDisabled, 5*time.Second)

	// A call to a disabled module is a degraded capability: Unavailable, not a
	// panic and not a hang.
	if _, err := s.Search(context.Background(), v1.SearchRequest{
		Caller: v1.CallerFromSession("h"), Text: "x",
	}); err == nil {
		t.Fatal("a disabled module answered a call")
	}

	// The admin was told, which is the third of platform#39's open crash-loop
	// questions.
	if !tel.sawError("module disabled after crash-looping") {
		t.Error("no telemetry error reported the disable")
	}
}

// The manifest is stable across a restart, so role resolution does not flicker
// while the process is down.
func TestManifestIsStableAcrossRestart(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "crashed")
	s, _ := supervise(t, []string{
		"EXTPROBE_EXIT_AFTER_MS=60",
		"EXTPROBE_CRASH_ONCE=" + marker,
	}, fastPolicy())

	before := s.Manifest()

	// Read the manifest continuously across the crash and restart; it must never
	// go blank, or a role-resolution pass landing in that window would drop the
	// module's roles.
	deadline := time.Now().Add(2 * time.Second)
	sawRestart := false
	for time.Now().Before(deadline) {
		if s.State() == extension.StateRestarting {
			sawRestart = true
		}
		if got := s.Manifest(); got.ID != before.ID {
			t.Fatalf("manifest id changed mid-life: %q -> %q", before.ID, got.ID)
		}
		if sawRestart && s.State() == extension.StateRunning {
			return // observed a full down-and-up with a stable manifest.
		}
		time.Sleep(5 * time.Millisecond)
	}
	if !sawRestart {
		t.Fatal("never observed the restart the test depends on")
	}
}

// A backoff means the restart is not instant: a module dying repeatedly does not
// spin the CPU. Measured indirectly — three crashes before disable must take at
// least the summed backoffs.
func TestRestartsBackOff(t *testing.T) {
	policy := fastPolicy()
	start := time.Now()
	s, _ := supervise(t, []string{"EXTPROBE_EXIT_AFTER_MS=20"}, policy)

	waitForState(t, s, extension.StateDisabled, 5*time.Second)
	elapsed := time.Since(start)

	// Three failures at 20ms, 40ms, 80ms(capped) backoff = 140ms of waiting at
	// minimum, plus the run time before each crash. A generous floor that still
	// proves the delays are real rather than instant.
	const floor = 100 * time.Millisecond
	if elapsed < floor {
		t.Errorf("crash-loop to disable took %v, under the %v floor — backoff is not being applied", elapsed, floor)
	}
}

// recordingTelemetry captures lifecycle events so a test can assert an admin was
// told.
type recordingTelemetry struct {
	mu     sync.Mutex
	errors []string
	warns  []string
}

func (r *recordingTelemetry) Debug(string, ...v1.Field) {}
func (r *recordingTelemetry) Info(string, ...v1.Field)  {}

func (r *recordingTelemetry) Warn(msg string, _ ...v1.Field) {
	r.mu.Lock()
	r.warns = append(r.warns, msg)
	r.mu.Unlock()
}

func (r *recordingTelemetry) Error(msg string, _ ...v1.Field) {
	r.mu.Lock()
	r.errors = append(r.errors, msg)
	r.mu.Unlock()
}

func (r *recordingTelemetry) Span(ctx context.Context, _ string, _ ...v1.Field) (context.Context, v1.Span) {
	return ctx, noopSpan{}
}

func (r *recordingTelemetry) Count(string, int64, ...v1.Field)              {}
func (r *recordingTelemetry) Measure(string, float64, v1.Unit, ...v1.Field) {}

func (r *recordingTelemetry) sawError(msg string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.errors {
		if e == msg {
			return true
		}
	}
	return false
}

type noopSpan struct{}

func (noopSpan) SetAttributes(...v1.Field) {}
func (noopSpan) Fail(error)                {}
func (noopSpan) End()                      {}
