package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// The association-graph and identity commands.

// twoWorks seeds a fixture with two works, returning the service, the
// caller's session and the two work ids.
func twoWorks(t *testing.T) (*app.Service, *fakeDB, domain.SessionID, domain.NodeID, domain.NodeID) {
	t.Helper()
	svc, db, _, session := commandFixture(t)
	ctx := context.Background()
	a, err := svc.AddContentWork(ctx, app.AddContentWorkCommand{
		CallerSessionID: session, MediaType: domain.MediaAnimeSeries, Title: "Fullmetal Alchemist: Brotherhood",
	})
	if err != nil {
		t.Fatalf("seed anime: %v", err)
	}
	b, err := svc.AddContentWork(ctx, app.AddContentWorkCommand{
		CallerSessionID: session, MediaType: domain.MediaMangaSeries, Title: "Fullmetal Alchemist",
	})
	if err != nil {
		t.Fatalf("seed manga: %v", err)
	}
	return svc, db, session, a.Work.ID, b.Work.ID
}

func TestRelateContent(t *testing.T) {
	ctx := context.Background()

	t.Run("draws an edge between two works", func(t *testing.T) {
		svc, db, session, anime, manga := twoWorks(t)
		res, err := svc.RelateContent(ctx, app.RelateContentCommand{
			CallerSessionID: session, FromNodeID: anime, ToNodeID: manga,
			Type: domain.RelationAdaptation, Confidence: 0.98, Origin: domain.OriginProviderSupplied,
		})
		if err != nil {
			t.Fatalf("RelateContent: %v", err)
		}
		if res.Relation.FromNodeID != anime || res.Relation.ToNodeID != manga {
			t.Fatalf("edge = %+v", res.Relation)
		}
		if !db.outboxHas("content.relation.created") {
			t.Fatalf("missing relation event: %v", db.outboxTypes())
		}
	})

	t.Run("rejects a self loop", func(t *testing.T) {
		svc, _, session, anime, _ := twoWorks(t)
		_, err := svc.RelateContent(ctx, app.RelateContentCommand{
			CallerSessionID: session, FromNodeID: anime, ToNodeID: anime,
			Type: domain.RelationSequel, Confidence: 1, Origin: domain.OriginUserConfirmed,
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})

	t.Run("rejects an unknown type, origin, or out-of-range confidence", func(t *testing.T) {
		svc, _, session, anime, manga := twoWorks(t)
		base := app.RelateContentCommand{
			CallerSessionID: session, FromNodeID: anime, ToNodeID: manga,
			Type: domain.RelationAdaptation, Confidence: 1, Origin: domain.OriginProviderSupplied,
		}
		bad := base
		bad.Type = "invented"
		if got := contracts.CategoryOf(mustErrRelate(svc.RelateContent(ctx, bad))); got != contracts.InvalidArgument {
			t.Fatalf("unknown type: %s", got)
		}
		bad = base
		bad.Origin = "guessed"
		if got := contracts.CategoryOf(mustErrRelate(svc.RelateContent(ctx, bad))); got != contracts.InvalidArgument {
			t.Fatalf("unknown origin: %s", got)
		}
		bad = base
		bad.Confidence = 1.5
		if got := contracts.CategoryOf(mustErrRelate(svc.RelateContent(ctx, bad))); got != contracts.InvalidArgument {
			t.Fatalf("bad confidence: %s", got)
		}
	})

	t.Run("a missing endpoint is not found", func(t *testing.T) {
		svc, _, session, anime, _ := twoWorks(t)
		_, err := svc.RelateContent(ctx, app.RelateContentCommand{
			CallerSessionID: session, FromNodeID: anime, ToNodeID: "n-missing",
			Type: domain.RelationAdaptation, Confidence: 1, Origin: domain.OriginProviderSupplied,
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want not_found", got)
		}
	})

	t.Run("a duplicate edge is a conflict", func(t *testing.T) {
		svc, _, session, anime, manga := twoWorks(t)
		cmd := app.RelateContentCommand{
			CallerSessionID: session, FromNodeID: anime, ToNodeID: manga,
			Type: domain.RelationAdaptation, Confidence: 1, Origin: domain.OriginProviderSupplied,
		}
		if _, err := svc.RelateContent(ctx, cmd); err != nil {
			t.Fatalf("first RelateContent: %v", err)
		}
		if got := contracts.CategoryOf(mustErrRelate(svc.RelateContent(ctx, cmd))); got != contracts.Conflict {
			t.Fatalf("category = %s, want conflict", got)
		}
	})
}

func TestBindContentSource(t *testing.T) {
	ctx := context.Background()

	t.Run("binds a confirmed source to a node", func(t *testing.T) {
		svc, db, session, anime, _ := twoWorks(t)
		res, err := svc.BindContentSource(ctx, app.BindContentSourceCommand{
			CallerSessionID: session, NodeID: anime,
			SourceProvider: "anilist", SourceRef: "5114",
			MatchConfidence: 1, MatchMethod: domain.MatchExternalIDExact, Status: domain.BindingConfirmed,
		})
		if err != nil {
			t.Fatalf("BindContentSource: %v", err)
		}
		if res.Binding.NodeID != anime {
			t.Fatalf("binding node = %q, want %q", res.Binding.NodeID, anime)
		}
		if !db.outboxHas("content.source.bound") {
			t.Fatalf("missing bind event: %v", db.outboxTypes())
		}
	})

	t.Run("a weak match may be queued for review", func(t *testing.T) {
		svc, _, session, anime, _ := twoWorks(t)
		res, err := svc.BindContentSource(ctx, app.BindContentSourceCommand{
			CallerSessionID: session, NodeID: anime,
			SourceProvider: "fuzzy", SourceRef: "maybe", MatchConfidence: 0.4,
			MatchMethod: domain.MatchFuzzyTitle, Status: domain.BindingPendingReview,
		})
		if err != nil {
			t.Fatalf("BindContentSource: %v", err)
		}
		if !res.Binding.NeedsReview() {
			t.Fatalf("status = %q, want pending_review", res.Binding.Status)
		}
	})

	t.Run("a new binding cannot be created rejected", func(t *testing.T) {
		svc, _, session, anime, _ := twoWorks(t)
		_, err := svc.BindContentSource(ctx, app.BindContentSourceCommand{
			CallerSessionID: session, NodeID: anime,
			SourceProvider: "x", SourceRef: "y", MatchConfidence: 0,
			MatchMethod: domain.MatchFuzzyTitle, Status: domain.BindingRejected,
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})

	t.Run("binding a missing node is not found", func(t *testing.T) {
		svc, _, session, _, _ := twoWorks(t)
		_, err := svc.BindContentSource(ctx, app.BindContentSourceCommand{
			CallerSessionID: session, NodeID: "n-missing",
			SourceProvider: "x", SourceRef: "y", MatchConfidence: 1,
			MatchMethod: domain.MatchExternalIDExact, Status: domain.BindingConfirmed,
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want not_found", got)
		}
	})

	t.Run("one source binds to at most one node", func(t *testing.T) {
		svc, _, session, anime, manga := twoWorks(t)
		cmd := app.BindContentSourceCommand{
			CallerSessionID: session, NodeID: anime,
			SourceProvider: "anilist", SourceRef: "5114", MatchConfidence: 1,
			MatchMethod: domain.MatchExternalIDExact, Status: domain.BindingConfirmed,
		}
		if _, err := svc.BindContentSource(ctx, cmd); err != nil {
			t.Fatalf("first bind: %v", err)
		}
		cmd.NodeID = manga
		if got := contracts.CategoryOf(mustErrBind(svc.BindContentSource(ctx, cmd))); got != contracts.Conflict {
			t.Fatalf("category = %s, want conflict", got)
		}
	})
}

func TestResolveContentBinding(t *testing.T) {
	ctx := context.Background()

	// seedPending returns a service, session, the binding id and the two work
	// ids, with one weak binding queued against the first work.
	seedPending := func(t *testing.T) (*app.Service, domain.SessionID, domain.SourceBindingID, domain.NodeID, domain.NodeID) {
		t.Helper()
		svc, _, session, wrong, right := twoWorks(t)
		res, err := svc.BindContentSource(ctx, app.BindContentSourceCommand{
			CallerSessionID: session, NodeID: wrong,
			SourceProvider: "fuzzy", SourceRef: "thing-2011", MatchConfidence: 0.5,
			MatchMethod: domain.MatchFuzzyTitle, Status: domain.BindingPendingReview,
		})
		if err != nil {
			t.Fatalf("seed binding: %v", err)
		}
		return svc, session, res.Binding.ID, wrong, right
	}

	t.Run("confirm settles the binding", func(t *testing.T) {
		svc, session, binding, _, _ := seedPending(t)
		res, err := svc.ResolveContentBinding(ctx, app.ResolveContentBindingCommand{
			CallerSessionID: session, BindingID: binding, Resolution: app.ResolveConfirm,
		})
		if err != nil {
			t.Fatalf("ResolveContentBinding: %v", err)
		}
		if res.Binding.Status != domain.BindingConfirmed {
			t.Fatalf("status = %q, want confirmed", res.Binding.Status)
		}
	})

	t.Run("reject keeps the row but declines the match", func(t *testing.T) {
		svc, session, binding, _, _ := seedPending(t)
		res, err := svc.ResolveContentBinding(ctx, app.ResolveContentBindingCommand{
			CallerSessionID: session, BindingID: binding, Resolution: app.ResolveReject,
		})
		if err != nil {
			t.Fatalf("ResolveContentBinding: %v", err)
		}
		if res.Binding.Status != domain.BindingRejected {
			t.Fatalf("status = %q, want rejected", res.Binding.Status)
		}
	})

	// A split moves the binding to a different node and confirms it, without
	// re-resolving the source's identity.
	t.Run("a split moves the binding and confirms it", func(t *testing.T) {
		svc, session, binding, wrong, right := seedPending(t)
		res, err := svc.ResolveContentBinding(ctx, app.ResolveContentBindingCommand{
			CallerSessionID: session, BindingID: binding, Resolution: app.ResolveConfirm, MoveToNodeID: right,
		})
		if err != nil {
			t.Fatalf("ResolveContentBinding: %v", err)
		}
		if res.Binding.NodeID != right {
			t.Fatalf("binding node = %q, want %q", res.Binding.NodeID, right)
		}
		if res.Binding.Status != domain.BindingConfirmed {
			t.Fatalf("status = %q, want confirmed", res.Binding.Status)
		}
		// Identity survived the move untouched.
		if res.Binding.MatchMethod != domain.MatchFuzzyTitle || res.Binding.MatchConfidence != 0.5 {
			t.Fatalf("a split re-resolved identity: %+v", res.Binding)
		}
		_ = wrong
	})

	t.Run("a rejection cannot also move", func(t *testing.T) {
		svc, session, binding, _, right := seedPending(t)
		_, err := svc.ResolveContentBinding(ctx, app.ResolveContentBindingCommand{
			CallerSessionID: session, BindingID: binding, Resolution: app.ResolveReject, MoveToNodeID: right,
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})

	t.Run("splitting onto a missing node is not found", func(t *testing.T) {
		svc, session, binding, _, _ := seedPending(t)
		_, err := svc.ResolveContentBinding(ctx, app.ResolveContentBindingCommand{
			CallerSessionID: session, BindingID: binding, Resolution: app.ResolveConfirm, MoveToNodeID: "n-missing",
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want not_found", got)
		}
	})

	t.Run("resolving a missing binding is not found", func(t *testing.T) {
		svc, session, _, _, _ := seedPending(t)
		_, err := svc.ResolveContentBinding(ctx, app.ResolveContentBindingCommand{
			CallerSessionID: session, BindingID: "b-missing", Resolution: app.ResolveConfirm,
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want not_found", got)
		}
	})
}

func mustErrRelate(_ app.RelateContentResult, err error) error   { return err }
func mustErrBind(_ app.BindContentSourceResult, err error) error { return err }
