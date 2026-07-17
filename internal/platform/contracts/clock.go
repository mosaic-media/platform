package contracts

import "time"

// Clock provides a deterministic time boundary (MEG-015 §03). Domain and
// application code must call Clock.Now instead of time.Now directly so
// tests can substitute a fixed clock (MEG-004 §04 — Driven Ports).
type Clock interface {
	Now() time.Time
}
