// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Playback state (ADR 0046) — the first per-user surface on the content
// service.
//
// Everything else here operates on an install-global graph. These five methods
// operate on one person's relationship to it, which is why they authorise
// against the caller's own identity and can never be asked about somebody
// else's: there is no user parameter to pass.

// ActionPlaybackWrite is the policy action for recording a viewer's own
// position.
//
// Separate from `content.create` deliberately. Writing progress is not editing
// the library — a household member who may watch everything and change nothing
// is an ordinary arrangement, and collapsing the two would make resume a
// librarian's privilege.
const ActionPlaybackWrite policy.Action = "playback.write"

// ActionPlaybackRead is the policy action for reading it back. It is separate
// from `content.read` for the same reason inverted: seeing the library is not
// the same as seeing what someone watched.
const ActionPlaybackRead policy.Action = "playback.read"

// finishedFraction is how far through an item counts as finished.
//
// Ninety-five percent rather than the end, because nobody watches the credits
// and an item that can only be finished by reaching its final frame never gets
// marked. It is deliberately a fraction rather than a fixed tail: a
// twenty-minute episode's credits are not a two-hour film's, and a fixed ninety
// seconds would mark half a short finished.
const finishedFraction = 0.95

// minFinishedDuration guards the fraction against a player that reports a
// nonsense duration.
//
// Without it, a duration of a few seconds — which a player emits briefly while
// metadata loads — makes almost any position 95% of the way through, and an item
// marks itself finished the instant it starts.
const minFinishedDuration = 30 * time.Second

// RecordPlaybackProgress records where a viewer has got to (ADR 0046).
//
// It is called repeatedly during playback, so it is an upsert and it is cheap.
// The transport coalesces bursts before they arrive here; this end assumes it
// may still be called often and does nothing per call that it would not want
// done a hundred times.
func (s *Service) RecordPlaybackProgress(ctx context.Context, cmd v1.RecordPlaybackProgressCommand) (v1.RecordPlaybackProgressResult, error) {
	// 1. validate command shape.
	if cmd.Caller.Session == "" {
		return v1.RecordPlaybackProgressResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.NodeID == "" {
		return v1.RecordPlaybackProgressResult{}, contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	if cmd.Position < 0 || cmd.Duration < 0 {
		return v1.RecordPlaybackProgressResult{}, contracts.NewError(contracts.InvalidArgument, "position and duration cannot be negative")
	}

	// 2-3. authenticate the caller and authorize the action.
	az, err := s.enter(ctx, cmd.Caller, ActionPlaybackWrite, policy.Resource{Type: "playback"})
	if err != nil {
		return v1.RecordPlaybackProgressResult{}, err
	}

	var result v1.RecordPlaybackProgressResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load state through contracts. A first report has none, which is
		// the normal case rather than an error.
		previous, err := tx.PlaybackStates().Get(ctx, az.userID, cmd.NodeID)
		if err != nil && contracts.CategoryOf(err) != contracts.NotFound {
			return err
		}

		// 6. apply domain rules.
		state := v1.PlaybackState{
			NodeID:           cmd.NodeID,
			PartID:           cmd.PartID,
			Position:         cmd.Position,
			Duration:         cmd.Duration,
			FinishedExplicit: previous.FinishedExplicit,
			UpdatedAt:        s.clock.Now(),
		}
		// A duration of zero means the player has not worked it out yet. Keeping
		// the last one it knew stops a mid-playback report from erasing the
		// length and taking the finished threshold with it.
		if state.Duration == 0 {
			state.Duration = previous.Duration
		}
		// Finished is derived, unless a person has already decided. Their answer
		// is never re-derived away in either direction — which is the whole
		// reason the explicit flag exists.
		if previous.FinishedExplicit {
			state.Finished = previous.Finished
		} else {
			state.Finished = crossedFinishThreshold(state.Position, state.Duration)
		}

		// 7. persist state and the outbox event in the same transaction.
		saved, err := tx.PlaybackStates().Upsert(ctx, az.userID, state)
		if err != nil {
			return err
		}
		// The event announces the *item*, not the position. A position moves
		// every few seconds and nothing downstream wants that firehose; what a
		// consumer wants to know is that this user watched this thing.
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "content.playback.progressed", []byte(string(saved.NodeID)), string(az.userID)),
		}); err != nil {
			return err
		}

		result = v1.RecordPlaybackProgressResult{State: saved}
		return nil
	})
	if err != nil {
		return v1.RecordPlaybackProgressResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}

// SetPlaybackFinished marks an item watched or unwatched by explicit request.
//
// It is a distinct command from progress because it is a distinct act: progress
// is something a player observed, and this is something a person decided. If
// they were one command, every position report would be able to silently
// un-mark something a viewer had marked.
func (s *Service) SetPlaybackFinished(ctx context.Context, cmd v1.SetPlaybackFinishedCommand) (v1.SetPlaybackFinishedResult, error) {
	if cmd.Caller.Session == "" {
		return v1.SetPlaybackFinishedResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.NodeID == "" {
		return v1.SetPlaybackFinishedResult{}, contracts.NewError(contracts.InvalidArgument, "node id is required")
	}

	az, err := s.enter(ctx, cmd.Caller, ActionPlaybackWrite, policy.Resource{Type: "playback"})
	if err != nil {
		return v1.SetPlaybackFinishedResult{}, err
	}

	var result v1.SetPlaybackFinishedResult

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		previous, err := tx.PlaybackStates().Get(ctx, az.userID, cmd.NodeID)
		if err != nil && contracts.CategoryOf(err) != contracts.NotFound {
			return err
		}

		state := previous
		state.NodeID = cmd.NodeID
		state.Finished = cmd.Finished
		state.FinishedExplicit = true
		state.UpdatedAt = s.clock.Now()
		// Marking something unwatched puts it back at the beginning. Leaving the
		// position would produce an item that claims to be unwatched and resumes
		// at the credits, which is the only reading of "unwatched" nobody means.
		if !cmd.Finished {
			state.Position = 0
		}

		saved, err := tx.PlaybackStates().Upsert(ctx, az.userID, state)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "content.playback.marked", []byte(string(saved.NodeID)), string(az.userID)),
		}); err != nil {
			return err
		}

		result = v1.SetPlaybackFinishedResult{State: saved}
		return nil
	})
	if err != nil {
		return v1.SetPlaybackFinishedResult{}, err
	}

	return result, nil
}

// GetPlaybackState reads one viewer's position in one item.
func (s *Service) GetPlaybackState(ctx context.Context, q v1.GetPlaybackStateQuery) (v1.GetPlaybackStateResult, error) {
	if q.Caller.Session == "" {
		return v1.GetPlaybackStateResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.NodeID == "" {
		return v1.GetPlaybackStateResult{}, contracts.NewError(contracts.InvalidArgument, "node id is required")
	}

	az, err := s.enter(ctx, q.Caller, ActionPlaybackRead, policy.Resource{Type: "playback"})
	if err != nil {
		return v1.GetPlaybackStateResult{}, err
	}
	if s.playbackStates == nil {
		return v1.GetPlaybackStateResult{}, errNoPlaybackStore
	}

	state, err := s.playbackStates.Get(ctx, az.userID, q.NodeID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			// Never started is an answer, not a failure. Reporting it as one
			// would make a detail screen for an unwatched film an error state.
			return v1.GetPlaybackStateResult{}, nil
		}
		return v1.GetPlaybackStateResult{}, err
	}
	return v1.GetPlaybackStateResult{State: state, Found: true}, nil
}

// ListPlaybackStates reads state for several items at once — a season's watched
// marks in one query rather than one per episode.
func (s *Service) ListPlaybackStates(ctx context.Context, q v1.ListPlaybackStatesQuery) (v1.ListPlaybackStatesResult, error) {
	if q.Caller.Session == "" {
		return v1.ListPlaybackStatesResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	az, err := s.enter(ctx, q.Caller, ActionPlaybackRead, policy.Resource{Type: "playback"})
	if err != nil {
		return v1.ListPlaybackStatesResult{}, err
	}
	if s.playbackStates == nil {
		return v1.ListPlaybackStatesResult{}, errNoPlaybackStore
	}
	if len(q.NodeIDs) == 0 {
		return v1.ListPlaybackStatesResult{States: map[v1.NodeID]v1.PlaybackState{}}, nil
	}

	states, err := s.playbackStates.ListByNodes(ctx, az.userID, q.NodeIDs)
	if err != nil {
		return v1.ListPlaybackStatesResult{}, err
	}
	return v1.ListPlaybackStatesResult{States: states}, nil
}

// ListInProgress reads what a viewer has started and not finished, most recent
// first — the continue-watching list.
//
// It resolves each state's node here rather than leaving a caller to do it,
// because every caller would, and doing it once in a batch is the difference
// between one query and one per rail item.
func (s *Service) ListInProgress(ctx context.Context, q v1.ListInProgressQuery) (v1.ListInProgressResult, error) {
	if q.Caller.Session == "" {
		return v1.ListInProgressResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	az, err := s.enter(ctx, q.Caller, ActionPlaybackRead, policy.Resource{Type: "playback"})
	if err != nil {
		return v1.ListInProgressResult{}, err
	}
	if s.playbackStates == nil || s.nodes == nil {
		return v1.ListInProgressResult{}, errNoPlaybackStore
	}

	states, err := s.playbackStates.ListInProgress(ctx, az.userID, q.Limit)
	if err != nil {
		return v1.ListInProgressResult{}, err
	}

	items := make([]v1.InProgressItem, 0, len(states))
	for _, state := range states {
		node, err := s.nodes.FindByID(ctx, state.NodeID)
		if err != nil {
			// A position whose node has gone is a row the cascade should have
			// removed. Skipping it keeps the rail rendering rather than failing
			// the whole screen for one stale entry.
			if contracts.CategoryOf(err) == contracts.NotFound {
				continue
			}
			return v1.ListInProgressResult{}, err
		}
		items = append(items, v1.InProgressItem{Node: node, State: state})
	}
	return v1.ListInProgressResult{Items: items}, nil
}

// errNoPlaybackStore reports a Service wired without the playback store.
//
// It is Unavailable rather than an empty answer, and the distinction is not
// pedantry: an empty answer means "you have not watched this", which is a
// perfectly ordinary thing for a screen to render — so a forgotten wiring would
// present as a resume feature that silently never works, on a build where every
// test passed. It is exactly the failure the unreachable-capability register
// exists to catch, and this is the version a compiler cannot.
var errNoPlaybackStore = contracts.NewError(contracts.Unavailable, "no playback state store configured")

// crossedFinishThreshold decides whether a position counts as having finished
// an item.
//
// It refuses to decide at all without a credible duration. A player reports a
// duration of zero — or of a few seconds — while it is still loading metadata,
// and at those values almost any position is 95% of the way through, so an item
// would mark itself finished the instant it started.
func crossedFinishThreshold(position, duration time.Duration) bool {
	if duration < minFinishedDuration {
		return false
	}
	return float64(position) >= float64(duration)*finishedFraction
}
