// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// UpgradeStore persists what somebody asked the Supervisor to install
// (ADR 0129).
//
// **This is the one remedy on the resolution register the Platform cannot
// perform.** It cannot stop and restart itself onto a different Generation, so
// an upgrade is recorded here, read over the private handoff listener, and
// carried out by the process that can. The store is deliberately tiny: one
// pending request at a time, and settlement is a comparison rather than an
// acknowledgement.
type UpgradeStore interface {
	// Request records that somebody asked for a version, replacing whatever was
	// pending. Replacing rather than refusing, because the second press is the
	// one that reflects what the person currently wants — and there can be only
	// one pending request, since two would be applied by one Supervisor in an
	// order nobody chose.
	Request(ctx context.Context, request domain.UpgradeRequest) error
	// Pending returns the request waiting to be carried out. A NotFound error
	// is the ordinary case: nothing is waiting.
	Pending(ctx context.Context) (domain.UpgradeRequest, error)
	// Settle closes the pending request. It is called when the install is found
	// to be running the version that was asked for, which is a statement about
	// what *is* rather than a report of what happened — the process that would
	// have reported it has been replaced by the upgrade.
	Settle(ctx context.Context) error
}
