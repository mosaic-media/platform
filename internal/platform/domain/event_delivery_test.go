// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain_test

import (
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

func TestDeliveryPolicySchedulesExponentialBackoff(t *testing.T) {
	policy := domain.DeliveryPolicy{MaxAttempts: 8, BaseDelay: time.Minute, MaxDelay: time.Hour}
	now := time.Date(2026, 7, 17, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		attempts  int
		wantDelay time.Duration
	}{
		{1, 1 * time.Minute},
		{2, 2 * time.Minute},
		{3, 4 * time.Minute},
		{4, 8 * time.Minute},
		{5, 16 * time.Minute},
		{6, 32 * time.Minute},
		{7, time.Hour}, // 64 min capped to 60
	}
	for _, tt := range tests {
		next, dead := policy.Schedule(tt.attempts, now)
		if dead {
			t.Fatalf("attempts=%d: unexpectedly dead-lettered", tt.attempts)
		}
		if got := next.Sub(now); got != tt.wantDelay {
			t.Fatalf("attempts=%d: delay = %s, want %s", tt.attempts, got, tt.wantDelay)
		}
	}
}

func TestDeliveryPolicyDeadLettersAtMaxAttempts(t *testing.T) {
	policy := domain.DefaultDeliveryPolicy()
	now := time.Now()

	// One below the max still schedules a retry.
	if _, dead := policy.Schedule(policy.MaxAttempts-1, now); dead {
		t.Fatalf("attempts=%d should still retry", policy.MaxAttempts-1)
	}
	// At and beyond the max, dead-letter with a zero next-retry time.
	next, dead := policy.Schedule(policy.MaxAttempts, now)
	if !dead {
		t.Fatalf("attempts=%d should be dead-lettered", policy.MaxAttempts)
	}
	if !next.IsZero() {
		t.Fatalf("dead-lettered event should have zero next-retry, got %v", next)
	}
}

func TestDeliveryPolicyBackoffNeverExceedsMaxDelay(t *testing.T) {
	policy := domain.DeliveryPolicy{MaxAttempts: 100, BaseDelay: time.Second, MaxDelay: 30 * time.Second}
	now := time.Now()
	for attempts := 1; attempts < policy.MaxAttempts; attempts++ {
		next, dead := policy.Schedule(attempts, now)
		if dead {
			t.Fatalf("attempts=%d unexpectedly dead-lettered", attempts)
		}
		if delay := next.Sub(now); delay > policy.MaxDelay {
			t.Fatalf("attempts=%d: delay %s exceeds cap %s", attempts, delay, policy.MaxDelay)
		}
	}
}
