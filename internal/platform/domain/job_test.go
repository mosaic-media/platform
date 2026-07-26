// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain_test

import (
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The backoff is the difference between a queue that recovers from an upstream
// blip and one that hammers it, so its two boundary behaviours — that it grows,
// and that it stops growing — are asserted rather than assumed.

func TestBackoffGrowsExponentiallyAndStopsAtTheCeiling(t *testing.T) {
	b := domain.Backoff{Base: time.Second, Max: 8 * time.Second, Factor: 2}

	for _, tc := range []struct {
		attempt int
		want    time.Duration
	}{
		{1, time.Second},
		{2, 2 * time.Second},
		{3, 4 * time.Second},
		{4, 8 * time.Second},
		// Capped from here: a job that has failed ten times must not next be
		// tried in a fortnight.
		{5, 8 * time.Second},
		{40, 8 * time.Second},
	} {
		if got := b.Delay(tc.attempt); got != tc.want {
			t.Errorf("Delay(%d) = %s, want %s", tc.attempt, got, tc.want)
		}
	}
}

// A runaway attempt counter must not overflow into a negative delay, which
// would be a job retried instantly forever — the exact failure a backoff exists
// to prevent, arrived at through the backoff.
func TestBackoffCannotProduceANegativeDelay(t *testing.T) {
	b := domain.Backoff{Base: time.Hour, Max: 0, Factor: 2}
	if got := b.Delay(1 << 20); got < 0 {
		t.Fatalf("Delay overflowed to %s", got)
	}
}

func TestBackoffJitterStaysWithinItsFraction(t *testing.T) {
	// Both extremes of the draw, so the bound is asserted rather than sampled.
	for _, draw := range []float64{0, 1} {
		b := domain.DefaultBackoff(func() float64 { return draw })
		got := b.Delay(1)
		base := float64(30 * time.Second)
		lo := time.Duration(base * 0.9)
		hi := time.Duration(base * 1.1)
		if got < lo || got > hi {
			t.Fatalf("draw %v: Delay(1) = %s, want within [%s, %s]", draw, got, lo, hi)
		}
	}
}

// A zero-value Backoff must be deterministic: a test that set no rand source
// should not get a jittered answer, and DefaultBackoff(nil) is what the runner
// takes when nothing configured one.
func TestBackoffWithoutARandSourceIsExact(t *testing.T) {
	b := domain.DefaultBackoff(nil)
	if got, want := b.Delay(1), 30*time.Second; got != want {
		t.Fatalf("Delay(1) = %s, want exactly %s", got, want)
	}
}

func TestJobExhaustedCountsTheAttemptJustSpent(t *testing.T) {
	if (domain.Job{Attempt: 4, MaxAttempts: 5}).Exhausted() {
		t.Error("a job with an attempt left reported exhausted")
	}
	if !(domain.Job{Attempt: 5, MaxAttempts: 5}).Exhausted() {
		t.Error("a job on its last attempt did not report exhausted")
	}
}
