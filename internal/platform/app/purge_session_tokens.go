// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionSessionMaintain is the install's own housekeeping over the session
// credential tables — not a permission over anybody's session.
//
// Its own action rather than a reuse of `user.session.revoke`, because deleting
// a token that expired last week is not revoking a session and reading it as
// though it were would put a maintenance sweep behind a permission about ending
// somebody's access. It sits in the superuser preset beside the other
// install-level actions, and in practice the only caller is the system
// principal.
const ActionSessionMaintain policy.Action = "session.maintain"

// PurgeSessionTokensCommand is one sweep of the expired credential tables.
type PurgeSessionTokensCommand struct {
	Caller v1.Caller
}

// PurgeSessionTokensResult is how many rows went.
type PurgeSessionTokensResult struct {
	Deleted int
}

// PurgeSessionTokens deletes access and refresh tokens that are past their
// lifetime (platform#58).
//
// It exists because the access-token table is the fastest-growing thing in the
// schema: one row per client per ten minutes, forever, and nothing else would
// ever remove them. The rows are useless the moment they expire — validation
// checks the expiry — so this is disk and index size rather than correctness,
// which is exactly the kind of slow problem that is invisible until it is not.
//
// It is a DELETE rather than a partition drop, unlike telemetry retention. The
// tables are small relative to telemetry, the rows are removed by expiry rather
// than by day, and partitioning them would be machinery in service of a volume
// that does not exist.
func (s *Service) PurgeSessionTokens(ctx context.Context, cmd PurgeSessionTokensCommand) (PurgeSessionTokensResult, error) {
	if cmd.Caller.Session == "" {
		return PurgeSessionTokensResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if _, err := s.enter(ctx, cmd.Caller, ActionSessionMaintain, policy.Resource{Type: "session"}); err != nil {
		return PurgeSessionTokensResult{}, err
	}
	if s.tokens == nil {
		return PurgeSessionTokensResult{}, contracts.NewError(contracts.Unavailable, "this Platform issues no session credentials")
	}

	deleted, err := s.tokens.DeleteExpired(ctx, s.clock.Now())
	if err != nil {
		return PurgeSessionTokensResult{}, err
	}
	if deleted > 0 {
		telemetry.From(ctx).For("sessions").Info("deleted expired session tokens",
			telemetry.Int("rows", deleted))
	}
	return PurgeSessionTokensResult{Deleted: deleted}, nil
}
