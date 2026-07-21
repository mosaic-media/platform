// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// systemClock is the runtime contracts.Clock: real wall-clock time in UTC.
//
// The clock is a runtime concern rather than a PostgreSQL one. It is provided
// here as part of the built-in module bundle so the composition root can
// assemble a complete set of driven-port implementations; it has no dependency
// on the database.
type systemClock struct{}

// NewClock returns the runtime system clock.
func NewClock() contracts.Clock {
	return systemClock{}
}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}
