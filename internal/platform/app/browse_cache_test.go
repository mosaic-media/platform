// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Cache-first browse reads (platform#30).
//
// The behaviour under test is the one the empty home screen came from: a source
// that does not answer must not be able to make a full library look unconfigured.
// Each case below is one way that used to happen.

// downableCapability is a catalog provider with a switch. Flipping `down` makes
// every call fail the way an unreachable addon does, which is what these tests
// need and what the fixture's provider cannot do.
type downableCapability struct {
	id       string
	catalogs []v1.Catalog
	items    []v1.CatalogItem
	down     bool
	calls    int
}

func (c *downableCapability) Manifest() v1.Manifest {
	return v1.Manifest{ID: c.id, Version: "0.0.1", Name: "Downable", Provides: []v1.Role{v1.RoleCatalog}}
}

func (c *downableCapability) Import(context.Context, v1.ContentService, v1.ImportRequest) (v1.ImportResult, error) {
	return v1.ImportResult{}, nil
}

func (c *downableCapability) Catalogs(context.Context, v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	c.calls++
	if c.down {
		return v1.CatalogsResponse{}, errors.New("dial tcp: connection refused")
	}
	return v1.CatalogsResponse{Catalogs: c.catalogs}, nil
}

func (c *downableCapability) CatalogItems(context.Context, v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	c.calls++
	if c.down {
		return v1.CatalogItemsResponse{}, errors.New("dial tcp: connection refused")
	}
	return v1.CatalogItemsResponse{Items: c.items}, nil
}

// snapshotFixture is providerFixture with a clock a test can place, so the same
// fakeDB can be read again from later in time and the freshness window is
// exercised rather than assumed.
func snapshotFixture(t *testing.T, cap v1.Capability, now time.Time) (*app.Service, *fakeDB, v1.Caller) {
	t.Helper()
	db := newFakeDB()
	registry := app.NewCapabilityRegistry()
	registry.Register(cap)
	svc := newTestServiceWithCapabilities(db, &trace{}, now, registry)
	db.seedUser(domain.User{ID: "u-1", Username: "curator", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	return svc, db, v1.Caller{Session: "s-1"}
}

// serviceAt rebuilds a Service over the same fake storage at a different moment,
// which is how a stored answer is aged without waiting. The session is re-seeded
// at the same moment, because the credential is minutes-lived (platform#58) and
// travelling an hour forward would otherwise expire it — a real client refreshes
// across that gap, and this is the fixture's version of the same thing.
func serviceAt(db *fakeDB, cap v1.Capability, now time.Time) *app.Service {
	registry := app.NewCapabilityRegistry()
	registry.Register(cap)
	db.seedSession("s-1", "u-1", now)
	return newTestServiceWithCapabilities(db, &trace{}, now, registry)
}

// TestBrowseServesTheSnapshotWhenTheSourceIsDown is the defect in one test. The
// source answered once, then stopped answering; the second read must return the
// same rows rather than nothing, and must say which source failed — because a
// screen that cannot tell the difference renders "add an addon" to an install
// that has one.
func TestBrowseServesTheSnapshotWhenTheSourceIsDown(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{
		id:       "fake",
		catalogs: []v1.Catalog{{ID: "top", NativeType: "movie", Name: "Popular"}},
		items:    []v1.CatalogItem{{Ref: searchRef("movie", "tt1"), Title: "A Film"}},
	}
	svc, db, caller := snapshotFixture(t, cap, now)

	warm, err := svc.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("warm read: %v", err)
	}
	if warm.Answer.From != app.AnswerLive || len(warm.Items) != 1 {
		t.Fatalf("warm = %+v, want one live item", warm.Answer)
	}

	// The source goes away, and enough time passes that the stored answer is no
	// longer served without asking.
	cap.down = true
	cold := serviceAt(db, cap, now.Add(time.Hour))
	got, err := cold.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
		Refresh: true,
	})
	if err != nil {
		t.Fatalf("cold read: %v", err)
	}
	if len(got.Items) != 1 || got.Items[0].Title != "A Film" {
		t.Fatalf("items = %+v, want the stored answer", got.Items)
	}
	if got.Answer.From != app.AnswerSnapshot {
		t.Fatalf("answer.From = %q, want %q", got.Answer.From, app.AnswerSnapshot)
	}
	if len(got.Answer.Failed) != 1 || got.Answer.Failed[0] != "fake" {
		t.Fatalf("answer.Failed = %v, want [fake] — a failure that is not named cannot be reported", got.Answer.Failed)
	}
	if !got.Answer.TakenAt.Equal(now) {
		t.Fatalf("answer.TakenAt = %v, want %v — a stale screen has to be able to say how old it is", got.Answer.TakenAt, now)
	}
}

// TestBrowseWithNoSnapshotAsksAndWaits covers the cold install: there is nothing
// stored, so the read goes to the source and the caller waits. That render is
// the only slow one, and it must leave a snapshot behind.
func TestBrowseWithNoSnapshotAsksAndWaits(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{id: "fake", down: true}
	svc, _, caller := snapshotFixture(t, cap, now)

	got, err := svc.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("cold read: %v", err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("items = %+v, want none — there is nothing stored to serve", got.Items)
	}
	if len(got.Answer.Failed) != 1 {
		t.Fatalf("answer.Failed = %v, want the source named: an empty page and a failed one are different states",
			got.Answer.Failed)
	}
}

// TestBrowseServesAFreshSnapshotWithoutAsking is the property that keeps this
// cheap. A viewer returning to home a minute later must not fan out to every
// provider again — and must not be told the screen is stale, because it is not.
func TestBrowseServesAFreshSnapshotWithoutAsking(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{
		id:    "fake",
		items: []v1.CatalogItem{{Ref: searchRef("movie", "tt1"), Title: "A Film"}},
	}
	svc, db, caller := snapshotFixture(t, cap, now)
	if _, err := svc.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	}); err != nil {
		t.Fatalf("warm read: %v", err)
	}
	calls := cap.calls

	soon := serviceAt(db, cap, now.Add(time.Minute))
	got, err := soon.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("second read: %v", err)
	}
	if cap.calls != calls {
		t.Fatalf("provider calls = %d, want %d — a fresh snapshot must not cost a round trip", cap.calls, calls)
	}
	if got.Answer.From != app.AnswerSnapshot || got.Answer.Stale {
		t.Fatalf("answer = %+v, want a fresh snapshot", got.Answer)
	}

	// Past the window it is still served immediately, but now it asks to be
	// revalidated — which is what schedules the background refresh.
	later := serviceAt(db, cap, now.Add(30*time.Minute))
	stale, err := later.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("stale read: %v", err)
	}
	if !stale.Answer.Stale {
		t.Fatalf("answer = %+v, want stale past the freshness window", stale.Answer)
	}
	if cap.calls != calls {
		t.Fatalf("provider calls = %d, want %d — a stale read still serves without waiting", cap.calls, calls)
	}
}

// TestBrowseRefreshReplacesTheStoredAnswer proves revalidation actually
// revalidates: the refresh asks, and what it is told replaces what was stored.
func TestBrowseRefreshReplacesTheStoredAnswer(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{
		id:    "fake",
		items: []v1.CatalogItem{{Ref: searchRef("movie", "tt1"), Title: "Yesterday's Ranking"}},
	}
	svc, db, caller := snapshotFixture(t, cap, now)
	if _, err := svc.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	}); err != nil {
		t.Fatalf("warm read: %v", err)
	}

	cap.items = []v1.CatalogItem{{Ref: searchRef("movie", "tt2"), Title: "Today's Ranking"}}
	later := serviceAt(db, cap, now.Add(time.Hour))
	refreshed, err := later.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie", Refresh: true,
	})
	if err != nil {
		t.Fatalf("refresh: %v", err)
	}
	if len(refreshed.Items) != 1 || refreshed.Items[0].Title != "Today's Ranking" {
		t.Fatalf("items = %+v, want the live answer", refreshed.Items)
	}
	if refreshed.Answer.From != app.AnswerLive {
		t.Fatalf("answer.From = %q, want live", refreshed.Answer.From)
	}

	// And the store now holds it, so the next cold render draws today's.
	afterwards := serviceAt(db, cap, now.Add(time.Hour))
	stored, err := afterwards.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(stored.Items) != 1 || stored.Items[0].Title != "Today's Ranking" {
		t.Fatalf("stored = %+v, want the refreshed answer", stored.Items)
	}
}

// TestBrowseRederivesInLibrary is the one field a snapshot must never restore.
// In-library is a fact about this install's own graph, not about the source's
// answer, so a title added after the snapshot was taken has to draw as owned.
func TestBrowseRederivesInLibrary(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{
		id:    "fake",
		items: []v1.CatalogItem{{Ref: searchRef("movie", "tt1254207"), Title: "Blade Runner 2049"}},
	}
	svc, db, caller := snapshotFixture(t, cap, now)
	if _, err := svc.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	}); err != nil {
		t.Fatalf("warm read: %v", err)
	}

	// Added to the library *after* the snapshot was taken.
	db.seedNode(v1.Node{
		ID: "n-2", WorkID: "n-2", Kind: v1.NodeWork, MediaType: v1.MediaMovie,
		Title: "Blade Runner 2049", Status: v1.NodeActive, ExternalIDs: []byte(`{"imdb":"tt1254207"}`),
	})
	cap.down = true
	later := serviceAt(db, cap, now.Add(time.Hour))
	got, err := later.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("stale read: %v", err)
	}
	if len(got.Items) != 1 || !got.Items[0].InLibrary || got.Items[0].NodeID != "n-2" {
		t.Fatalf("item = %+v, want it marked in-library from the graph rather than from the snapshot", got.Items)
	}
}

// TestBrowseCatalogsSurvivesAColdManifest covers the other half of the empty
// home screen: an addon's *catalog list* comes out of a manifest fetched over
// the network, so a cold source has no catalogs at all — and a home screen with
// no catalogs renders "add an addon in Settings" to an install that has one.
func TestBrowseCatalogsSurvivesAColdManifest(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{
		id:       "fake",
		catalogs: []v1.Catalog{{ID: "top", NativeType: "movie", Name: "Popular"}},
	}
	svc, db, caller := snapshotFixture(t, cap, now)
	if _, err := svc.BrowseCatalogs(ctx, app.BrowseCatalogsQuery{Caller: caller}); err != nil {
		t.Fatalf("warm read: %v", err)
	}

	cap.down = true
	cold := serviceAt(db, cap, now.Add(time.Hour))
	got, err := cold.BrowseCatalogs(ctx, app.BrowseCatalogsQuery{Caller: caller, Refresh: true})
	if err != nil {
		t.Fatalf("cold read: %v", err)
	}
	if len(got.Catalogs) != 1 || got.Catalogs[0].Catalog.ID != "top" {
		t.Fatalf("catalogs = %+v, want the stored list", got.Catalogs)
	}
	if got.Answer.From != app.AnswerSnapshot || len(got.Answer.Failed) != 1 {
		t.Fatalf("answer = %+v, want a snapshot with the source named", got.Answer)
	}
}

// TestBrowseStoresAnEmptyAnswer is the mirror of the watch-provider refresh's
// rule, and it is load-bearing for the same reason: a catalog that has genuinely
// emptied must be able to say so. Skipping the empty answer would freeze the
// last non-empty one forever, which is a screen confidently showing content that
// is no longer there.
func TestBrowseStoresAnEmptyAnswer(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 7, 27, 12, 0, 0, 0, time.UTC)
	cap := &downableCapability{
		id:    "fake",
		items: []v1.CatalogItem{{Ref: searchRef("movie", "tt1"), Title: "A Film"}},
	}
	svc, db, caller := snapshotFixture(t, cap, now)
	if _, err := svc.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	}); err != nil {
		t.Fatalf("warm read: %v", err)
	}

	cap.items = nil
	later := serviceAt(db, cap, now.Add(time.Hour))
	if _, err := later.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie", Refresh: true,
	}); err != nil {
		t.Fatalf("refresh: %v", err)
	}

	cap.down = true
	afterwards := serviceAt(db, cap, now.Add(2*time.Hour))
	got, err := afterwards.BrowseCatalogItems(ctx, app.BrowseCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if len(got.Items) != 0 {
		t.Fatalf("items = %+v, want none — the empty answer must have overwritten the old one", got.Items)
	}
}
