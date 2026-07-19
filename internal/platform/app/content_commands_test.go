package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// The content write commands. These prove the command boundary — validate,
// authenticate, authorise, transact, emit — and the domain shape each command
// imposes. The storage behaviour underneath is the contract suite's job.

func commandFixture(t *testing.T) (*app.Service, *fakeDB, *trace, domain.SessionID) {
	t.Helper()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	tr := &trace{}
	svc := newTestService(db, tr, now)
	db.seedUser(domain.User{ID: "u-1", Username: "curator", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	return svc, db, tr, "s-1"
}

func TestAddContentWork(t *testing.T) {
	svc, db, _, session := commandFixture(t)
	ctx := context.Background()

	t.Run("creates a root work that roots its own tree", func(t *testing.T) {
		result, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaAnimeSeries, Title: "Cowboy Bebop",
		})
		if err != nil {
			t.Fatalf("AddContentWork: %v", err)
		}
		work := result.Work
		if work.Kind != v1.NodeWork {
			t.Fatalf("kind = %q, want work", work.Kind)
		}
		if !work.IsRoot() {
			t.Fatal("a work must have no parent")
		}
		if work.WorkID != work.ID {
			t.Fatalf("work id %q should equal its own id %q", work.WorkID, work.ID)
		}
		if work.Status != v1.NodeActive {
			t.Fatalf("status = %q, want active", work.Status)
		}
	})

	t.Run("emits content.work.created in the same transaction", func(t *testing.T) {
		if _, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaMovie, Title: "Akira",
		}); err != nil {
			t.Fatalf("AddContentWork: %v", err)
		}
		if !db.outboxHas("content.work.created") {
			t.Fatalf("outbox missing the work event: %v", db.outboxTypes())
		}
	})

	t.Run("normalises the media type", func(t *testing.T) {
		result, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string(session)}, MediaType: "Anime Series", Title: "Trigun",
		})
		if err != nil {
			t.Fatalf("AddContentWork: %v", err)
		}
		if result.Work.MediaType != v1.MediaAnimeSeries {
			t.Fatalf("media type = %q, want normalised", result.Work.MediaType)
		}
	})

	t.Run("requires a media type and a title", func(t *testing.T) {
		_, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{Caller: v1.Caller{Session: string(session)}, Title: "x"})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("missing media type: category = %s", got)
		}
		_, err = svc.AddContentWork(ctx, v1.AddContentWorkCommand{Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaMovie})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("missing title: category = %s", got)
		}
	})

	t.Run("an unauthorised caller cannot create and writes nothing", func(t *testing.T) {
		now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
		db := newFakeDB()
		tr := &trace{}
		svc := newTestService(db, tr, now)
		db.seedUser(domain.User{ID: "u-2", Username: "guest", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
		db.seedSession("s-2", "u-2", now)

		_, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string("s-2")}, MediaType: v1.MediaMovie, Title: "Denied",
		})
		if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
			t.Fatalf("category = %s, want permission_denied", got)
		}
		for _, step := range tr.snapshot() {
			if step == "nodes.create" {
				t.Fatalf("a denied caller reached the store: %v", tr.snapshot())
			}
		}
	})
}

func TestAddContentChild(t *testing.T) {
	svc, _, _, session := commandFixture(t)
	ctx := context.Background()

	seedWork := func(t *testing.T) v1.Node {
		t.Helper()
		res, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaTVSeries, Title: "Severance",
		})
		if err != nil {
			t.Fatalf("seed work: %v", err)
		}
		return res.Work
	}

	t.Run("inherits work id and media type from the parent", func(t *testing.T) {
		work := seedWork(t)
		res, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: v1.Caller{Session: string(session)}, ParentID: work.ID,
			Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason,
			Title: "Season 1", NaturalOrder: 1,
		})
		if err != nil {
			t.Fatalf("AddContentChild: %v", err)
		}
		child := res.Node
		if child.WorkID != work.ID {
			t.Fatalf("work id = %q, want the parent's %q", child.WorkID, work.ID)
		}
		if child.MediaType != work.MediaType {
			t.Fatalf("media type = %q, want inherited %q", child.MediaType, work.MediaType)
		}
		if child.ParentID == nil || *child.ParentID != work.ID {
			t.Fatalf("parent id = %v, want %q", child.ParentID, work.ID)
		}
	})

	t.Run("a missing parent is not found", func(t *testing.T) {
		_, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: v1.Caller{Session: string(session)}, ParentID: "n-missing",
			Kind: v1.NodeItem, ItemType: v1.ItemEpisode, Title: "Orphan",
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want not_found", got)
		}
	})

	t.Run("a work cannot be added as a child", func(t *testing.T) {
		work := seedWork(t)
		_, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: v1.Caller{Session: string(session)}, ParentID: work.ID, Kind: v1.NodeWork, Title: "Nested",
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})

	t.Run("the type field must match the kind", func(t *testing.T) {
		work := seedWork(t)
		// A container carrying an item type.
		_, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: v1.Caller{Session: string(session)}, ParentID: work.ID,
			Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason, ItemType: v1.ItemEpisode,
			Title: "Bad",
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
		// An item with no item type.
		_, err = svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: v1.Caller{Session: string(session)}, ParentID: work.ID, Kind: v1.NodeItem, Title: "No type",
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})
}

func TestAttachContentPart(t *testing.T) {
	svc, _, _, session := commandFixture(t)
	ctx := context.Background()

	// A work with one item beneath it, returning the item.
	seedItem := func(t *testing.T) v1.Node {
		t.Helper()
		work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaMovie, Title: "Blade Runner 2049",
		})
		if err != nil {
			t.Fatalf("seed work: %v", err)
		}
		item, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: v1.Caller{Session: string(session)}, ParentID: work.Work.ID,
			Kind: v1.NodeItem, ItemType: v1.ItemFeature, Title: "Blade Runner 2049",
		})
		if err != nil {
			t.Fatalf("seed item: %v", err)
		}
		return item.Node
	}

	t.Run("attaches a local part to an item", func(t *testing.T) {
		item := seedItem(t)
		res, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
			Caller: v1.Caller{Session: string(session)}, NodeID: item.ID, Role: v1.PartEdition,
			Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/media/br2049.mkv"},
			Duration: 2 * time.Hour,
		})
		if err != nil {
			t.Fatalf("AttachContentPart: %v", err)
		}
		if res.Part.NodeID != item.ID {
			t.Fatalf("part node = %q, want %q", res.Part.NodeID, item.ID)
		}
	})

	t.Run("refuses to attach to a work or container", func(t *testing.T) {
		work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
			Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaTVSeries, Title: "Severance",
		})
		if err != nil {
			t.Fatalf("seed work: %v", err)
		}
		_, err = svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
			Caller: v1.Caller{Session: string(session)}, NodeID: work.Work.ID, Role: v1.PartEdition,
			Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/media/x.mkv"},
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want invalid_argument", got)
		}
	})

	t.Run("a remote location requires a provider, a local one forbids it", func(t *testing.T) {
		item := seedItem(t)
		_, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
			Caller: v1.Caller{Session: string(session)}, NodeID: item.ID, Role: v1.PartEdition,
			Location: v1.MediaLocation{Scheme: v1.RemoteLocation, Ref: "magnet:?x"},
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("remote without provider: category = %s", got)
		}
		_, err = svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
			Caller: v1.Caller{Session: string(session)}, NodeID: item.ID, Role: v1.PartEdition,
			Location: v1.MediaLocation{Scheme: v1.LocalLocation, Provider: "debrid", Ref: "/x.mkv"},
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("local with provider: category = %s", got)
		}
	})
}

// The point of the transaction: a command that fails after writing must leave
// nothing behind — not the node, not the event. A part attached to a work
// fails inside the transaction, after the node lookup, so the fake's rollback
// is what has to hold.
func TestContentCommandRollsBackAtomically(t *testing.T) {
	svc, db, _, session := commandFixture(t)
	ctx := context.Background()

	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: v1.Caller{Session: string(session)}, MediaType: v1.MediaMovie, Title: "Arrival",
	})
	if err != nil {
		t.Fatalf("seed work: %v", err)
	}
	outboxBefore := len(db.outboxTypes())

	// Attaching to the work (not an item) fails after the node is loaded and
	// before anything commits.
	_, err = svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
		Caller: v1.Caller{Session: string(session)}, NodeID: work.Work.ID, Role: v1.PartEdition,
		Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/x.mkv"},
	})
	if contracts.CategoryOf(err) != contracts.InvalidArgument {
		t.Fatalf("expected invalid_argument, got %v", err)
	}

	if got := len(db.parts); got != 0 {
		t.Fatalf("a part committed despite the failure: %d", got)
	}
	if got := len(db.outboxTypes()); got != outboxBefore {
		t.Fatalf("an event committed despite the failure: %d, want %d", got, outboxBefore)
	}
}
