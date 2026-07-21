// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package events

import (
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// systemClock is the default contracts.Clock a Worker/Bus uses for health
// bookkeeping timestamps when no clock is injected via WithClock. Real
// wall-clock time in UTC, mirroring internal/modules/postgres's own
// systemClock — duplicated rather than imported, since internal/platform
// (this package) must not depend on internal/modules/postgres, per the
// inward dependency rule.
type systemClock struct{}

func (systemClock) Now() time.Time { return time.Now().UTC() }

var _ contracts.Clock = systemClock{}
