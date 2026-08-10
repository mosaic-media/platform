// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"connectrpc.com/connect"

	"github.com/mosaic-media/platform/internal/transport/clientaddr"
)

// The rate limit on the pre-session surface (platform#57).
//
// It is a token bucket per peer, and it is deliberately the smallest thing that
// answers the requirement rather than a general-purpose limiter. A general one
// belongs in front of the whole API — the egress proxy has the same gap
// recorded against it — and inventing the policy for it here, with one caller,
// is how a mechanism ends up shaped by its first use.
//
// What it does buy: an unauthenticated caller cannot loop this call to make the
// Platform read the user store and marshal the component library without bound.

// bootstrapRate is how many bootstraps one peer may make per minute, and the
// burst it may take at once.
//
// Generous, because a legitimate client makes exactly one per page load and a
// developer with hot reload makes a handful a minute. It is a ceiling on abuse,
// not a throttle on use — a limit a real client can reach is a limit that will
// be raised without thought the first time somebody hits it.
const (
	bootstrapPerMinute = 60
	bootstrapBurst     = 20
)

// limiter is a per-key token bucket.
type limiter struct {
	perMinute float64
	burst     float64

	mu      sync.Mutex
	buckets map[string]*bucket
	// lastSweep is when stale buckets were last dropped. Without it the map is
	// an unbounded allocation keyed by whatever a caller can vary, which is the
	// shape of a rate limiter that is itself the denial of service.
	lastSweep time.Time
}

type bucket struct {
	tokens float64
	seen   time.Time
}

func newLimiter(perMinute, burst float64) *limiter {
	return &limiter{perMinute: perMinute, burst: burst, buckets: map[string]*bucket{}}
}

// allow spends a token for key, refilling by elapsed time first.
func (l *limiter) allow(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	l.sweep(now)

	b, ok := l.buckets[key]
	if !ok {
		b = &bucket{tokens: l.burst, seen: now}
		l.buckets[key] = b
	}
	if elapsed := now.Sub(b.seen); elapsed > 0 {
		b.tokens += elapsed.Minutes() * l.perMinute
		if b.tokens > l.burst {
			b.tokens = l.burst
		}
	}
	b.seen = now
	if b.tokens < 1 {
		return false
	}
	b.tokens--
	return true
}

// bucketTTL is how long a peer's bucket is kept after its last request. Longer
// than the time it takes a full bucket to refill, so dropping one can never
// hand back more budget than waiting would have.
const bucketTTL = 10 * time.Minute

func (l *limiter) sweep(now time.Time) {
	if now.Sub(l.lastSweep) < bucketTTL {
		return
	}
	l.lastSweep = now
	for k, b := range l.buckets {
		if now.Sub(b.seen) > bucketTTL {
			delete(l.buckets, k)
		}
	}
}

// peerOf is the key a request is limited under: the caller's address without
// its port, so one client's many connections share one bucket.
//
// It reads what the transport resolved (internal/transport/clientaddr) rather
// than the connection's own peer, because behind the front door those are not
// the same thing. Every request now arrives from the Supervisor, and over a
// Unix socket there is no peer address at all — so keying on the connection
// would put the whole household in one bucket, which is the failure this
// exists to prevent rather than a lesser version of it.
//
// The header is believed only on a listener that cannot be reached except
// through the front door; clientaddr.Middleware is where that is decided, and
// it is a parameter there rather than a guess here.
func peerOf[T any](ctx context.Context, _ *connect.Request[T]) string {
	return clientaddr.From(ctx)
}

// countDefinitions reports how many components a served payload carries, for
// the telemetry line that says how much of the library the doorway needed. A
// malformed payload counts as none rather than failing the call — this is a
// number in a log line, not a check.
func countDefinitions(raw []byte) int {
	var defs []json.RawMessage
	if err := json.Unmarshal(raw, &defs); err != nil {
		return 0
	}
	return len(defs)
}
