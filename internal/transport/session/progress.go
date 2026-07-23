// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Progress coalescing (ADR 0046).
//
// A playing video reports where it has got to, and the naive wiring makes that a
// database write per second per viewer for the length of a film. The Platform
// already solves this shape once — search-as-you-type coalesces a burst of
// keystrokes into one render — and this is the same mechanism pointed at a
// different firehose.
//
// The difference from input is what happens at the end. A dropped keystroke is
// harmless because another follows; a dropped *final* position is the one that
// matters most, because it is where the viewer actually stopped. So the last
// report is always flushed rather than discarded, and the client sends one at
// the boundaries that matter — pause, seek settled, exit.

// progressDebounce is the coalescing window for position reports.
//
// Long, deliberately. Losing up to this much of a position costs a viewer a few
// seconds of rewatching; writing every report costs a row per second per stream.
// The client reports on a slower cadence than this anyway, so in practice this
// catches bursts — a seek, which emits several in quick succession — rather than
// the steady drip.
const progressDebounce = 5 * time.Second

// progressWriteTimeout bounds the write a debounce timer performs. The timer
// fires after the intent has returned, so it cannot use that intent's context.
const progressWriteTimeout = 10 * time.Second

// progressEnvelope is the reportProgress action input.
//
// Seconds as a float, because that is what a video element reports and
// converting in the client would be a place for two clients to disagree. The
// Platform converts once, here.
type progressEnvelope struct {
	NodeID   string  `json:"nodeId"`
	PartID   string  `json:"partId"`
	Position float64 `json:"position"`
	Duration float64 `json:"duration"`
	// Final marks a report the client considers the last of a playback — an
	// exit, or a pause. It bypasses coalescing, because the position a viewer
	// stopped at is the one worth having and waiting five seconds for it risks
	// losing it to a closed tab.
	Final bool `json:"final"`
}

// progressFromInput decodes a reportProgress envelope.
func progressFromInput(input []byte) (progressEnvelope, error) {
	var env progressEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return progressEnvelope{}, contracts.NewError(contracts.InvalidArgument, "report progress: input is not valid JSON")
		}
	}
	if env.NodeID == "" {
		return progressEnvelope{}, contracts.NewError(contracts.InvalidArgument, "report progress: a node id is required")
	}
	if env.Position < 0 || env.Duration < 0 {
		return progressEnvelope{}, contracts.NewError(contracts.InvalidArgument, "report progress: position and duration cannot be negative")
	}
	return env, nil
}

// command converts an envelope into the application command.
func (e progressEnvelope) command(caller v1.Caller) v1.RecordPlaybackProgressCommand {
	return v1.RecordPlaybackProgressCommand{
		Caller:   caller,
		NodeID:   v1.NodeID(e.NodeID),
		PartID:   v1.PartID(e.PartID),
		Position: seconds(e.Position),
		Duration: seconds(e.Duration),
	}
}

// seconds converts a client's float seconds into a Duration, rounded to the
// millisecond the store keeps. A float carried further than this is a value that
// compares unequal to itself across a round trip.
func seconds(v float64) time.Duration {
	if v <= 0 {
		return 0
	}
	return time.Duration(v*1000) * time.Millisecond
}

// reportProgress records where a viewer has got to, coalescing a burst.
//
// A non-final report arms a timer and returns; a final one cancels the timer and
// writes immediately. Either way the intent Acks straight away — a player must
// never wait on a database to keep playing.
func (h *Handler) reportProgress(ctx context.Context, s *liveSession, input []byte) error {
	env, err := progressFromInput(input)
	if err != nil {
		return err
	}

	if env.Final {
		s.cancelProgress()
		h.writeProgress(ctx, s.caller, env)
		return nil
	}
	s.armProgress(env, func(pending progressEnvelope) {
		writeCtx, cancel := context.WithTimeout(context.Background(), progressWriteTimeout)
		defer cancel()
		h.writeProgress(writeCtx, s.caller, pending)
	})
	return nil
}

// writeProgress performs the application write and swallows its failure.
//
// Swallowed because there is nothing a viewer can do about it and nothing worth
// interrupting a film to say. A lost position costs them the last few seconds of
// their place; a toast over the player costs them the scene.
func (h *Handler) writeProgress(ctx context.Context, caller v1.Caller, env progressEnvelope) {
	res, err := h.svc.RecordPlaybackProgress(ctx, env.command(caller))
	if err != nil {
		telemetry.From(ctx).For("playback").Warn("recording progress failed",
			telemetry.Identifier("node", env.NodeID),
			telemetry.Err(err))
		return
	}
	// Crossing the threshold is the one progress report worth a record: it is
	// the moment an item leaves the continue-watching rail, and "why did that
	// disappear" is otherwise unanswerable after the fact.
	if res.State.Finished {
		telemetry.From(ctx).For("playback").Info("item finished",
			telemetry.Identifier("node", env.NodeID),
			telemetry.Duration("position", res.State.Position.Round(time.Second)),
			telemetry.Duration("duration", res.State.Duration.Round(time.Second)),
			telemetry.Bool("explicit", res.State.FinishedExplicit))
	}
}

// armProgress records the latest position and (re)arms the coalescing timer, so
// a burst collapses to one write for the final value.
func (s *liveSession) armProgress(env progressEnvelope, write func(progressEnvelope)) {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	s.pendingProgress = env
	if s.progressTimer != nil {
		s.progressTimer.Stop()
	}
	s.progressTimer = time.AfterFunc(progressDebounce, func() {
		s.progressMu.Lock()
		pending := s.pendingProgress
		s.progressTimer = nil
		s.progressMu.Unlock()
		if pending.NodeID == "" {
			return
		}
		write(pending)
	})
}

// cancelProgress stops a pending coalesced write, so a final report supersedes
// rather than races the timer it was queued behind.
func (s *liveSession) cancelProgress() {
	s.progressMu.Lock()
	defer s.progressMu.Unlock()
	if s.progressTimer != nil {
		s.progressTimer.Stop()
		s.progressTimer = nil
	}
	s.pendingProgress = progressEnvelope{}
}

// flushProgress writes any pending position immediately.
//
// It runs when a session is reaped or the Platform shuts down, which are exactly
// the moments a debounced write would otherwise be dropped — and the position it
// is holding is the most recent one there is.
func (h *Handler) flushProgress(s *liveSession) {
	s.progressMu.Lock()
	pending := s.pendingProgress
	if s.progressTimer != nil {
		s.progressTimer.Stop()
		s.progressTimer = nil
	}
	s.pendingProgress = progressEnvelope{}
	s.progressMu.Unlock()

	if pending.NodeID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), progressWriteTimeout)
	defer cancel()
	h.writeProgress(ctx, s.caller, pending)
}

// markFinishedEnvelope is the setWatched action input.
type markFinishedEnvelope struct {
	NodeID   string `json:"nodeId"`
	Finished bool   `json:"finished"`
}

// setWatchedFromInput decodes a setWatched envelope.
func setWatchedFromInput(input []byte) (v1.SetPlaybackFinishedCommand, error) {
	var env markFinishedEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return v1.SetPlaybackFinishedCommand{}, contracts.NewError(contracts.InvalidArgument, "set watched: input is not valid JSON")
		}
	}
	if env.NodeID == "" {
		return v1.SetPlaybackFinishedCommand{}, contracts.NewError(contracts.InvalidArgument, "set watched: a node id is required")
	}
	return v1.SetPlaybackFinishedCommand{NodeID: v1.NodeID(env.NodeID), Finished: env.Finished}, nil
}

// resumeFor reads where this viewer got to in an item, for the Player node's
// starting offset (ADR 0047).
//
// A missing state, a failed read and a finished item all mean the same thing
// here — start at the beginning — which is why this returns a bare duration
// rather than an error. Failing a play because a resume offset could not be read
// would trade the whole feature for one of its refinements.
func (h *Handler) resumeFor(ctx context.Context, caller v1.Caller, nodeID string) time.Duration {
	if nodeID == "" {
		return 0
	}
	res, err := h.svc.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{
		Caller: caller, NodeID: v1.NodeID(nodeID),
	})
	if err != nil || !res.Found {
		return 0
	}
	return res.State.ResumeAt()
}

// compile-time proof the handler's service is the published content surface, so
// the progress path cannot drift onto a private method.
var _ interface {
	RecordPlaybackProgress(context.Context, v1.RecordPlaybackProgressCommand) (v1.RecordPlaybackProgressResult, error)
} = (*app.Service)(nil)
