package events

import (
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// systemClock is the default contracts.Clock a Worker/Bus uses for health
// bookkeeping timestamps when no clock is injected via WithClock. Real
// wall-clock time in UTC, mirroring internal/modules/postgres's own
// systemClock — duplicated rather than imported, since internal/platform
// (this package) must not depend on internal/modules/postgres (MEG-015
// §02 — dependencies point inward, Platform must not depend on a Module).
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

var _ contracts.Clock = systemClock{}
