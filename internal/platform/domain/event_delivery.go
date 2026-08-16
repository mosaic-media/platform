// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// DeliveryPolicy is the Platform's outbox retry and dead-letter rule: given
// the number of attempts made so far and the current time, it decides when the
// next attempt is due and whether the event has exhausted its retries. It is
// pure value logic, kept apart from the outbox worker that performs
// deliveries so the failure bookkeeping is testable on its own.
//
// One policy applies to every event class. Critical Platform events may
// warrant stricter rules than low-priority diagnostic ones; per-class policies
// do not exist yet.
type DeliveryPolicy struct {
	// MaxAttempts is the number of failed attempts after which an event is
	// dead-lettered rather than retried again.
	MaxAttempts int
	// BaseDelay is the delay before the first retry.
	BaseDelay time.Duration
	// MaxDelay caps the exponential backoff.
	MaxDelay time.Duration
}

// DefaultDeliveryPolicy allows up to eight attempts with exponential backoff
// from one minute, capped at one hour.
func DefaultDeliveryPolicy() DeliveryPolicy {
	return DeliveryPolicy{
		MaxAttempts: 8,
		BaseDelay:   time.Minute,
		MaxDelay:    time.Hour,
	}
}

// Schedule decides the outcome after a failed delivery attempt. attempts is
// the total number of attempts made so far including the one that just
// failed (i.e. the post-increment count). It returns the time the next
// attempt becomes due and whether the event should now be dead-lettered.
// When deadLettered is true the returned time is the zero Time and no further
// attempt should be scheduled.
func (p DeliveryPolicy) Schedule(attempts int, now time.Time) (nextRetry time.Time, deadLettered bool) {
	if attempts >= p.MaxAttempts {
		return time.Time{}, true
	}
	return now.Add(p.backoff(attempts)), false
}

// backoff returns BaseDelay * 2^(attempts-1), capped at MaxDelay. It grows
// the delay iteratively and stops at the cap, so it never overflows even for
// large attempt counts.
func (p DeliveryPolicy) backoff(attempts int) time.Duration {
	if attempts < 1 {
		attempts = 1
	}
	delay := p.BaseDelay
	for i := 1; i < attempts; i++ {
		delay *= 2
		if delay >= p.MaxDelay {
			return p.MaxDelay
		}
	}
	if delay > p.MaxDelay {
		return p.MaxDelay
	}
	return delay
}
