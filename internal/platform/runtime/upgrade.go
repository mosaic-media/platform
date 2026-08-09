// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime

import (
	"context"
	"os"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// The upgrade request, as the Supervisor reads it (ADR 0129).
//
// **The Platform records what somebody asked for and cannot carry it out.** It
// cannot stop and restart itself onto a different Generation, so the request
// crosses the private handoff listener to the process that can. This is the
// Platform's half: report what is pending, and settle it by observing that the
// install is now running the version that was asked for.

// generationIDEnv names the Generation this process belongs to. The Supervisor
// sets it on every child it starts.
//
// **Settlement is a comparison rather than an acknowledgement**, and that is
// why this variable is load-bearing. An upgrade replaces the process that would
// have reported success, and an activation that reverts would leave a report
// that was true for a minute — so what settles a request is the Platform being
// able to say "I am the version you asked for", which is a fact about now.
const generationIDEnv = "MOSAIC_GENERATION_ID"

// UpgradeStatus is what the handoff answers.
//
// Flat, with no schema version, like the Supervisor's spool at the other end
// and for the same reason: it is written by one binary and read by another that
// may have been upgraded independently, so the reader ignores what it does not
// understand rather than failing on it.
type UpgradeStatus struct {
	Pending     bool      `json:"pending"`
	Version     string    `json:"version,omitempty"`
	RequestedAt time.Time `json:"requestedAt,omitzero"`
}

// CheckUpgrade reports the pending request, settling it first when the install
// is already running what it asks for.
//
// Settling here rather than at boot is deliberate: the Supervisor asks this
// every few seconds, so the request stops being pending as soon as it is true —
// and a Platform that never restarted has nothing to settle, which is the same
// code path answering "no".
func CheckUpgrade(ctx context.Context, store contracts.UpgradeStore) UpgradeStatus {
	if store == nil {
		return UpgradeStatus{}
	}
	request, err := store.Pending(ctx)
	if err != nil {
		// Including NotFound, which is the ordinary case: nothing is waiting.
		return UpgradeStatus{}
	}

	// **An empty Generation id settles nothing.** A deployment that runs the
	// Platform itself sets none, and treating "unknown" as "not it" would leave
	// a request pending forever there — which is the honest outcome, since
	// nothing in that deployment was going to carry it out either.
	if running := os.Getenv(generationIDEnv); running != "" && running == request.Version {
		// Best-effort: failing to settle costs a repeated report, not an
		// upgrade, and there is nowhere useful to return this error to.
		_ = store.Settle(ctx)
		return UpgradeStatus{}
	}

	return UpgradeStatus{
		Pending:     true,
		Version:     request.Version,
		RequestedAt: request.RequestedAt,
	}
}
