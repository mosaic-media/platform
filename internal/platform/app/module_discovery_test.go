// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

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

// These tests cover the Platform's read-side fan-out to module provider roles
// (ADR 0027) — search and catalog browse — and the union that marks a virtual
// result in-library (ADR 0028). A fake capability fills the read roles; the
// Platform drives it.

// fakeProviderCapability fills the search and catalog roles with canned data and
// records what it was asked, so a test checks the Platform's fan-out and union
// rather than any real source.
type fakeProviderCapability struct {
	id            string
	results       []v1.SearchResult
	catalogs      []v1.Catalog
	items         []v1.CatalogItem
	gotSearchText string
}

func (c *fakeProviderCapability) Manifest() v1.Manifest {
	return v1.Manifest{ID: c.id, Version: "0.0.1", Name: "Fake Provider", Provides: []v1.Role{v1.RoleSearch, v1.RoleCatalog}}
}

func (c *fakeProviderCapability) Import(context.Context, v1.ContentService, v1.ImportRequest) (v1.ImportResult, error) {
	return v1.ImportResult{}, nil
}

func (c *fakeProviderCapability) Search(_ context.Context, req v1.SearchRequest) (v1.SearchResponse, error) {
	c.gotSearchText = req.Text
	return v1.SearchResponse{Results: c.results}, nil
}

func (c *fakeProviderCapability) Catalogs(context.Context, v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	return v1.CatalogsResponse{Catalogs: c.catalogs}, nil
}

func (c *fakeProviderCapability) CatalogItems(context.Context, v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	return v1.CatalogItemsResponse{Items: c.items}, nil
}

func providerFixture(t *testing.T, cap v1.Capability, withRole bool) (*app.Service, *fakeDB, domain.SessionID) {
	t.Helper()
	now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	registry := app.NewCapabilityRegistry()
	registry.Register(cap)
	svc := newTestServiceWithCapabilities(db, &trace{}, now, registry)
	db.seedUser(domain.User{ID: "u-1", Username: "curator", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	if withRole {
		db.seedRole("u-1", adminRole())
	}
	return svc, db, "s-1"
}

func searchRef(nativeType, id string) v1.ContentRef {
	return v1.ContentRef{Provider: "fake", NativeID: id, NativeType: nativeType, ExternalScheme: "imdb", ExternalID: id}
}

func TestSearchAvailableContent(t *testing.T) {
	ctx := context.Background()
	cap := &fakeProviderCapability{
		id: "fake",
		results: []v1.SearchResult{
			{Ref: searchRef("series", "tt0903747"), Title: "Breaking Bad"}, // already in the library
			{Ref: searchRef("movie", "tt1254207"), Title: "Blade Runner 2049"},
		},
	}
	svc, db, session := providerFixture(t, cap, true)
	// Seed a library Work bound to the first result's external id.
	db.seedNode(v1.Node{
		ID: "n-1", WorkID: "n-1", Kind: v1.NodeWork, MediaType: v1.MediaTVSeries,
		Title: "Breaking Bad", Status: v1.NodeActive, ExternalIDs: []byte(`{"imdb":"tt0903747"}`),
	})

	res, err := svc.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{
		Caller: v1.Caller{Session: string(session)}, Text: "breaking",
	})
	if err != nil {
		t.Fatalf("SearchAvailableContent: %v", err)
	}
	if cap.gotSearchText != "breaking" {
		t.Fatalf("provider saw text %q, want the query forwarded", cap.gotSearchText)
	}
	if len(res.Results) != 2 {
		t.Fatalf("results = %d, want 2", len(res.Results))
	}
	byID := map[string]v1.SearchResult{}
	for _, r := range res.Results {
		byID[r.Ref.ExternalID] = r
	}
	if got := byID["tt0903747"]; !got.InLibrary || got.NodeID != "n-1" {
		t.Fatalf("in-library result = %+v, want InLibrary with NodeID n-1", got)
	}
	if got := byID["tt1254207"]; got.InLibrary || got.NodeID != "" {
		t.Fatalf("new result = %+v, want not in library", got)
	}
}

func TestSearchAvailableContentRequiresText(t *testing.T) {
	cap := &fakeProviderCapability{id: "fake"}
	svc, _, session := providerFixture(t, cap, true)
	_, err := svc.SearchAvailableContent(context.Background(), app.SearchAvailableContentQuery{
		Caller: v1.Caller{Session: string(session)},
	})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Fatalf("category = %s, want InvalidArgument", got)
	}
}

func TestSearchAvailableContentAuthorises(t *testing.T) {
	cap := &fakeProviderCapability{id: "fake"}
	// No role granted, so the caller lacks content.read.
	svc, _, session := providerFixture(t, cap, false)
	_, err := svc.SearchAvailableContent(context.Background(), app.SearchAvailableContentQuery{
		Caller: v1.Caller{Session: string(session)}, Text: "x",
	})
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("category = %s, want PermissionDenied", got)
	}
	if cap.gotSearchText != "" {
		t.Fatal("the provider must not be called for an unauthorised caller")
	}
}

func TestModuleCatalogsAndItems(t *testing.T) {
	ctx := context.Background()
	cap := &fakeProviderCapability{
		id:       "fake",
		catalogs: []v1.Catalog{{ID: "top", NativeType: "movie", Name: "Popular"}},
		items: []v1.CatalogItem{
			{Ref: searchRef("movie", "tt1254207"), Title: "Blade Runner 2049"}, // in library
			{Ref: searchRef("movie", "tt0111161"), Title: "The Shawshank Redemption"},
		},
	}
	svc, db, session := providerFixture(t, cap, true)
	db.seedNode(v1.Node{
		ID: "n-2", WorkID: "n-2", Kind: v1.NodeWork, MediaType: v1.MediaMovie,
		Title: "Blade Runner 2049", Status: v1.NodeActive, ExternalIDs: []byte(`{"imdb":"tt1254207"}`),
	})
	caller := v1.Caller{Session: string(session)}

	cats, err := svc.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListModuleCatalogs: %v", err)
	}
	if len(cats.Catalogs) != 1 || cats.Catalogs[0].ModuleID != "fake" || cats.Catalogs[0].Catalog.ID != "top" {
		t.Fatalf("catalogs = %+v, want one fake/top catalog", cats.Catalogs)
	}

	items, err := svc.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
		Caller: caller, ModuleID: "fake", CatalogID: "top", NativeType: "movie",
	})
	if err != nil {
		t.Fatalf("ListCatalogItems: %v", err)
	}
	if len(items.Items) != 2 {
		t.Fatalf("items = %d, want 2", len(items.Items))
	}
	byID := map[string]v1.CatalogItem{}
	for _, it := range items.Items {
		byID[it.Ref.ExternalID] = it
	}
	if got := byID["tt1254207"]; !got.InLibrary || got.NodeID != "n-2" {
		t.Fatalf("in-library item = %+v, want InLibrary with NodeID n-2", got)
	}
	if got := byID["tt0111161"]; got.InLibrary {
		t.Fatalf("new item = %+v, want not in library", got)
	}
}

func TestListCatalogItemsUnknownModule(t *testing.T) {
	cap := &fakeProviderCapability{id: "fake"}
	svc, _, session := providerFixture(t, cap, true)
	_, err := svc.ListCatalogItems(context.Background(), app.ListCatalogItemsQuery{
		Caller: v1.Caller{Session: string(session)}, ModuleID: "nope", CatalogID: "top",
	})
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("category = %s, want NotFound", got)
	}
}
