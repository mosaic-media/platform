package app_test

import (
	"context"
	"testing"
	"time"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// The content query services. These prove the query boundary — validate,
// authenticate, authorise, then read — rather than the search itself, which
// the storage contract suite covers against real PostgreSQL.

func contentFixture(t *testing.T) (*app.Service, *fakeDB, *trace, domain.SessionID) {
	t.Helper()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, now)

	db.seedUser(domain.User{ID: "u-1", Username: "viewer", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())

	work := v1.Node{
		ID: "n-1", WorkID: "n-1", Kind: v1.NodeWork,
		MediaType: v1.MediaAnimeSeries, Title: "Fullmetal Alchemist: Brotherhood",
		Status: v1.NodeActive, ExternalIDs: []byte(`{"anilist":"5114"}`),
		CreatedAt: now, UpdatedAt: now,
	}
	parent := work.ID
	episode := v1.Node{
		ID: "n-2", WorkID: work.ID, ParentID: &parent, Kind: v1.NodeItem,
		MediaType: v1.MediaAnimeSeries, ItemType: v1.ItemEpisode,
		Title: "The First Day", NaturalOrder: 1, Status: v1.NodeActive,
		CreatedAt: now, UpdatedAt: now,
	}
	db.seedNode(work)
	db.seedNode(episode)

	return svc, db, tr, "s-1"
}

func TestSearchContentRequiresAuthenticationAndPolicy(t *testing.T) {
	t.Run("an unknown session is unauthenticated", func(t *testing.T) {
		svc, _, _, _ := contentFixture(t)
		_, err := svc.SearchContent(context.Background(), app.SearchContentQuery{
			CallerSessionID: "no-such-session", Title: "alchemist",
		})
		if got := contracts.CategoryOf(err); got != contracts.Unauthenticated {
			t.Fatalf("category = %s, want unauthenticated", got)
		}
	})

	t.Run("a caller without the action is denied", func(t *testing.T) {
		now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
		db := newFakeDB()
		svc := newTestService(db, &trace{}, now)
		db.seedUser(domain.User{ID: "u-2", Username: "nobody", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
		db.seedSession("s-2", "u-2", now)
		// Deliberately no role: default-deny must hold.

		_, err := svc.SearchContent(context.Background(), app.SearchContentQuery{CallerSessionID: "s-2"})
		if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
			t.Fatalf("category = %s, want permission_denied", got)
		}
	})

	t.Run("a missing session id is invalid", func(t *testing.T) {
		svc, _, _, _ := contentFixture(t)
		_, err := svc.SearchContent(context.Background(), app.SearchContentQuery{Title: "x"})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})

	t.Run("an unknown node kind is invalid", func(t *testing.T) {
		svc, _, _, session := contentFixture(t)
		_, err := svc.SearchContent(context.Background(), app.SearchContentQuery{
			CallerSessionID: session, Kind: "sideways",
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})
}

// The order matters as much as the outcome: a denied caller must not reach
// the store at all.
func TestSearchContentDeniedBeforeReading(t *testing.T) {
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, now)
	db.seedUser(domain.User{ID: "u-2", Username: "nobody", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-2", "u-2", now)

	if _, err := svc.SearchContent(context.Background(), app.SearchContentQuery{CallerSessionID: "s-2"}); err == nil {
		t.Fatal("expected the query to be denied")
	}
	for _, step := range tr.snapshot() {
		if step == "nodes.search" {
			t.Fatalf("the store was read despite denial: %v", tr.snapshot())
		}
	}
}

func TestSearchContentReturnsMatches(t *testing.T) {
	svc, _, _, session := contentFixture(t)

	result, err := svc.SearchContent(context.Background(), app.SearchContentQuery{
		CallerSessionID: session, Title: "alchemist", Kind: v1.NodeWork,
	})
	if err != nil {
		t.Fatalf("SearchContent: %v", err)
	}
	if len(result.Nodes) != 1 || result.Nodes[0].ID != "n-1" {
		t.Fatalf("result = %+v, want the work", result.Nodes)
	}
}

// An unspecified limit must become the default rather than reaching the
// store as zero, which the store rejects.
func TestSearchContentDefaultsAndClampsTheLimit(t *testing.T) {
	svc, _, _, session := contentFixture(t)
	ctx := context.Background()

	if _, err := svc.SearchContent(ctx, app.SearchContentQuery{CallerSessionID: session}); err != nil {
		t.Fatalf("an unspecified limit should default, got: %v", err)
	}
	// An absurd limit is clamped rather than refused.
	if _, err := svc.SearchContent(ctx, app.SearchContentQuery{CallerSessionID: session, Limit: 100000}); err != nil {
		t.Fatalf("an oversized limit should clamp, got: %v", err)
	}
}

func TestFindContentByExternalID(t *testing.T) {
	svc, _, _, session := contentFixture(t)
	ctx := context.Background()

	t.Run("finds a node by a provider identifier", func(t *testing.T) {
		result, err := svc.FindContentByExternalID(ctx, app.FindContentByExternalIDQuery{
			CallerSessionID: session, Scheme: "anilist", Value: "5114",
		})
		if err != nil {
			t.Fatalf("FindContentByExternalID: %v", err)
		}
		if len(result.Nodes) != 1 || result.Nodes[0].ID != "n-1" {
			t.Fatalf("result = %+v", result.Nodes)
		}
	})

	t.Run("an unknown identifier is an empty result, not an error", func(t *testing.T) {
		result, err := svc.FindContentByExternalID(ctx, app.FindContentByExternalIDQuery{
			CallerSessionID: session, Scheme: "anilist", Value: "999999",
		})
		if err != nil {
			t.Fatalf("FindContentByExternalID: %v", err)
		}
		if len(result.Nodes) != 0 {
			t.Fatalf("expected no matches, got %+v", result.Nodes)
		}
	})

	t.Run("a missing scheme or value is invalid", func(t *testing.T) {
		_, err := svc.FindContentByExternalID(ctx, app.FindContentByExternalIDQuery{
			CallerSessionID: session, Value: "5114",
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})
}

func TestGetContentNode(t *testing.T) {
	svc, _, _, session := contentFixture(t)
	ctx := context.Background()

	t.Run("reads one node without its children by default", func(t *testing.T) {
		result, err := svc.GetContentNode(ctx, app.GetContentNodeQuery{
			CallerSessionID: session, NodeID: "n-1",
		})
		if err != nil {
			t.Fatalf("GetContentNode: %v", err)
		}
		if result.Node.Title != "Fullmetal Alchemist: Brotherhood" {
			t.Fatalf("node = %+v", result.Node)
		}
		if result.Children != nil {
			t.Fatalf("children should be nil unless asked for, got %+v", result.Children)
		}
	})

	t.Run("returns direct children when asked", func(t *testing.T) {
		result, err := svc.GetContentNode(ctx, app.GetContentNodeQuery{
			CallerSessionID: session, NodeID: "n-1", WithChildren: true,
		})
		if err != nil {
			t.Fatalf("GetContentNode: %v", err)
		}
		if len(result.Children) != 1 || result.Children[0].ID != "n-2" {
			t.Fatalf("children = %+v", result.Children)
		}
	})

	t.Run("a childless node returns an empty slice, not nil", func(t *testing.T) {
		result, err := svc.GetContentNode(ctx, app.GetContentNodeQuery{
			CallerSessionID: session, NodeID: "n-2", WithChildren: true,
		})
		if err != nil {
			t.Fatalf("GetContentNode: %v", err)
		}
		if result.Children == nil {
			t.Fatal("children should be an empty slice so callers can range without a guard")
		}
		if len(result.Children) != 0 {
			t.Fatalf("children = %+v", result.Children)
		}
	})

	t.Run("a missing node is not found", func(t *testing.T) {
		_, err := svc.GetContentNode(ctx, app.GetContentNodeQuery{
			CallerSessionID: session, NodeID: "n-999",
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want not_found", got)
		}
	})
}
