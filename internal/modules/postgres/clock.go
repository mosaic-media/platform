package postgres

import (
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// systemClock is the runtime contracts.Clock: real wall-clock time in UTC.
//
// MEG-015 §03 attributes Clock to the "Runtime clock" rather than to
// PostgreSQL specifically. It is provided here as part of the built-in
// module bundle so the composition root can assemble a complete set of
// driven-port implementations for this slice; it has no dependency on the
// database.
type systemClock struct{}

// NewClock returns the runtime system clock.
func NewClock() contracts.Clock {
	return systemClock{}
}

func (systemClock) Now() time.Time {
	return time.Now().UTC()
}
