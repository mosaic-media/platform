// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// playbackFixture builds a Service with a caller granted the ordinary-account
// actions, which is the tier playback state belongs to (ADR 0046) — a household
// member who may watch everything and change nothing.
func playbackFixture(t *testing.T) (context.Context, *app.Service, *fakeDB, v1.Caller) {
	t.Helper()
	svc, db, _, sid := importFixture(t)
	return context.Background(), svc, db, caller(sid)
}

// TestProgressDerivesFinishedAtTheThreshold covers the ordinary path: a viewer
// works through an item and it marks itself watched near the end, without anyone
// pressing anything.
func TestProgressDerivesFinishedAtTheThreshold(t *testing.T) {
	ctx, svc, _, c := playbackFixture(t)
	const runtime = 100 * time.Minute

	early, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 10 * time.Minute, Duration: runtime,
	})
	if err != nil {
		t.Fatalf("RecordPlaybackProgress: %v", err)
	}
	if early.State.Finished {
		t.Error("ten minutes into a hundred marked the item finished")
	}
	if !early.State.InProgress() {
		t.Error("a started, unfinished item is not in progress")
	}

	// Credits, not the final frame. An item that can only be finished by
	// reaching its last second never gets marked, because nobody watches that
	// far.
	late, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 96 * time.Minute, Duration: runtime,
	})
	if err != nil {
		t.Fatalf("RecordPlaybackProgress: %v", err)
	}
	if !late.State.Finished {
		t.Error("96 of 100 minutes did not cross the finished threshold")
	}
	if late.State.FinishedExplicit {
		t.Error("a derived finish claimed to be explicit")
	}
	if late.State.InProgress() {
		t.Error("a finished item is still in progress")
	}
	// And it resumes at the beginning rather than in the credits.
	if late.State.ResumeAt() != 0 {
		t.Errorf("a finished item resumes at %v, want the beginning", late.State.ResumeAt())
	}
}

// TestProgressWillNotFinishOnAMissingDuration guards the failure a player causes
// on its own: it reports a duration of zero, or of a few seconds, while metadata
// loads. At those values almost any position is 95% of the way through, so
// without a floor an item marks itself finished the instant it starts.
func TestProgressWillNotFinishOnAMissingDuration(t *testing.T) {
	ctx, svc, _, c := playbackFixture(t)

	for name, cmd := range map[string]v1.RecordPlaybackProgressCommand{
		"no duration":    {Caller: c, NodeID: "node-1", Position: 3 * time.Second},
		"tiny duration":  {Caller: c, NodeID: "node-2", Position: 5 * time.Second, Duration: 5 * time.Second},
		"short duration": {Caller: c, NodeID: "node-3", Position: 20 * time.Second, Duration: 20 * time.Second},
	} {
		res, err := svc.RecordPlaybackProgress(ctx, cmd)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.State.Finished {
			t.Errorf("%s: marked finished on a duration no player had settled on", name)
		}
	}
}

// TestProgressKeepsTheLastKnownDuration covers the same player behaviour from
// the other side: a mid-playback report that has forgotten the duration must not
// erase it, taking the finished threshold with it.
func TestProgressKeepsTheLastKnownDuration(t *testing.T) {
	ctx, svc, _, c := playbackFixture(t)

	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: time.Minute, Duration: 100 * time.Minute,
	}); err != nil {
		t.Fatalf("first report: %v", err)
	}
	res, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 99 * time.Minute,
	})
	if err != nil {
		t.Fatalf("second report: %v", err)
	}
	if res.State.Duration != 100*time.Minute {
		t.Errorf("duration = %v, want the remembered 100m", res.State.Duration)
	}
	if !res.State.Finished {
		t.Error("the remembered duration did not feed the threshold")
	}
}

// TestExplicitMarkIsNotReDerived is the sticky half of "derived, then sticky".
// Someone who marks a film watched at twenty minutes — because they saw it years
// ago — must not have that undone by the position report that follows, and
// someone who marks a finished film unwatched must not have it re-finished the
// moment they open it.
func TestExplicitMarkIsNotReDerived(t *testing.T) {
	ctx, svc, _, c := playbackFixture(t)
	const runtime = 100 * time.Minute

	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 20 * time.Minute, Duration: runtime,
	}); err != nil {
		t.Fatalf("progress: %v", err)
	}
	marked, err := svc.SetPlaybackFinished(ctx, v1.SetPlaybackFinishedCommand{
		Caller: c, NodeID: "node-1", Finished: true,
	})
	if err != nil {
		t.Fatalf("SetPlaybackFinished: %v", err)
	}
	if !marked.State.Finished || !marked.State.FinishedExplicit {
		t.Fatalf("mark did not stick: %+v", marked.State)
	}

	// A later report from a player that is still open must not un-finish it.
	after, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 21 * time.Minute, Duration: runtime,
	})
	if err != nil {
		t.Fatalf("progress after mark: %v", err)
	}
	if !after.State.Finished {
		t.Error("a position report un-finished an item a person had marked")
	}

	// And the inverse: marked unwatched, then played to the end, stays unwatched
	// until someone says otherwise.
	unmarked, err := svc.SetPlaybackFinished(ctx, v1.SetPlaybackFinishedCommand{
		Caller: c, NodeID: "node-1", Finished: false,
	})
	if err != nil {
		t.Fatalf("unmark: %v", err)
	}
	if unmarked.State.Position != 0 {
		t.Errorf("marking unwatched left the position at %v", unmarked.State.Position)
	}
	replayed, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 99 * time.Minute, Duration: runtime,
	})
	if err != nil {
		t.Fatalf("progress after unmark: %v", err)
	}
	if replayed.State.Finished {
		t.Error("the threshold re-derived a finish a person had explicitly removed")
	}
}

// TestGetPlaybackStateDistinguishesNeverStarted proves Found carries the
// difference the zero value cannot — which is what a detail screen renders as
// Play versus Resume.
func TestGetPlaybackStateDistinguishesNeverStarted(t *testing.T) {
	ctx, svc, _, c := playbackFixture(t)

	res, err := svc.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{Caller: c, NodeID: "never-touched"})
	if err != nil {
		t.Fatalf("GetPlaybackState: %v", err)
	}
	if res.Found {
		t.Error("an item nobody started reported a state")
	}

	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 5 * time.Minute, Duration: time.Hour,
	}); err != nil {
		t.Fatalf("progress: %v", err)
	}
	res, err = svc.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{Caller: c, NodeID: "node-1"})
	if err != nil {
		t.Fatalf("GetPlaybackState: %v", err)
	}
	if !res.Found || res.State.ResumeAt() != 5*time.Minute {
		t.Errorf("state = %+v, want a resumable five minutes", res.State)
	}
}

// TestInProgressExcludesFinishedAndUnstarted pins what the continue-watching
// rail is allowed to contain. A finished item keeps its position — that is what
// makes finishing recoverable — so a rail built on position alone would show
// everything anyone had ever completed.
func TestInProgressExcludesFinishedAndUnstarted(t *testing.T) {
	ctx, svc, _, c := playbackFixture(t)

	// Started and going.
	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-1", Position: 10 * time.Minute, Duration: 100 * time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	// Watched to the end.
	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-2", Position: 99 * time.Minute, Duration: 100 * time.Minute,
	}); err != nil {
		t.Fatal(err)
	}
	// Opened and closed without watching anything.
	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: c, NodeID: "node-3", Position: 0, Duration: 100 * time.Minute,
	}); err != nil {
		t.Fatal(err)
	}

	res, err := svc.ListInProgress(ctx, v1.ListInProgressQuery{Caller: c})
	if err != nil {
		t.Fatalf("ListInProgress: %v", err)
	}
	for _, item := range res.Items {
		switch item.State.NodeID {
		case "node-2":
			t.Error("a finished item is in the continue-watching list")
		case "node-3":
			t.Error("an item opened at zero is in the continue-watching list")
		}
	}
}

// TestPlaybackStateIsPerUser is the property that makes this the per-user tier
// rather than another shared one: two people watching the same item hold two
// positions, and neither can read or write the other's.
func TestPlaybackStateIsPerUser(t *testing.T) {
	ctx, svc, db, first := playbackFixture(t)

	// A second account on the *same* Service, which is what makes this a real
	// test of per-user keying rather than of two isolated fixtures.
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	db.seedUser(domain.User{ID: "u-2", Username: "housemate", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-2", "u-2", now)
	db.seedRole("u-2", adminRole())
	second := caller("s-2")

	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: first, NodeID: "node-1", Position: 40 * time.Minute, Duration: 100 * time.Minute,
	}); err != nil {
		t.Fatalf("first viewer: %v", err)
	}

	// The second viewer sees nothing, on the same item, through the same
	// Service. There is also no parameter through which they *could* name the
	// first viewer's state — the structural half of the same guarantee.
	res, err := svc.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{Caller: second, NodeID: "node-1"})
	if err != nil {
		t.Fatalf("second viewer: %v", err)
	}
	if res.Found {
		t.Fatalf("one viewer read another's position: %+v", res.State)
	}

	// And their own progress on the same item is theirs alone.
	if _, err := svc.RecordPlaybackProgress(ctx, v1.RecordPlaybackProgressCommand{
		Caller: second, NodeID: "node-1", Position: 5 * time.Minute, Duration: 100 * time.Minute,
	}); err != nil {
		t.Fatalf("second viewer progress: %v", err)
	}
	back, err := svc.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{Caller: first, NodeID: "node-1"})
	if err != nil {
		t.Fatalf("first viewer re-read: %v", err)
	}
	if back.State.Position != 40*time.Minute {
		t.Errorf("first viewer's position is now %v; the second viewer overwrote it", back.State.Position)
	}
}
