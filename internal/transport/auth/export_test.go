// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth

import "time"

// The two hooks the external test package needs to exercise the rate limit
// without sleeping through it.
//
// They live in an _test.go file, so they are compiled into the test binary and
// are absent from the package a release links — the limiter's clock cannot be
// replaced by anything that ships.

// SetClockForTest replaces the clock the limiter reads.
func SetClockForTest(h *Handler, now func() time.Time) { h.now = now }

// BootstrapBurstForTest is how many requests one peer may make at once.
const BootstrapBurstForTest = bootstrapBurst
