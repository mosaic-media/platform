// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Storing what a source said, and growing the tree from it (ADR 0107).
//
// The two failures this closes were both found by opening a library the
// maintenance job had filled: a detail screen with nothing on it, and a series
// that gained a season and never grew.

// seriesModule is a module that materialises a series and describes it. Its
// episode list is a field, so a test can make the source gain a season between
// two runs — which is the thing that was broken and could not be seen from
// inside the Platform.
type seriesModule struct {
	id string
	// episodes is what the metadata role reports, and it is deliberately *not*
	// what Import builds: a real module builds the tree it knew about when the
	// title was first materialised and never revisits it.
	episodes []v1.EpisodePreview
	overview string
	// buildSeasons and buildPerSeason are the tree Import creates, so a test can
	// start from a series with one season and let the pass find the rest.
	buildSeasons   int
	buildPerSeason int
	// failMetadata makes the metadata role unreachable, for the case where a
	// cache must keep what it already has.
	failMetadata bool
}

func (m *seriesModule) Manifest() v1.Manifest {
	return v1.Manifest{
		ID: m.id, Version: "0.0.1", Name: "Series module",
		Provides: []v1.Role{v1.RoleMetadata, v1.RoleCatalog},
	}
}

func (m *seriesModule) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	found, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: req.Caller, Scheme: "imdb", Value: req.Ref.ExternalID,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	for _, node := range found.Nodes {
		if node.IsRoot() {
			// What every real module does, and the reason ADR 0107 exists: a
			// re-import stops here, so nothing the module knows can ever reach
			// the tree a second time.
			return v1.ImportResult{WorkID: node.ID, AlreadyKnown: true}, nil
		}
	}

	ids, _ := json.Marshal(map[string]string{"imdb": req.Ref.ExternalID})
	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: req.Caller, MediaType: v1.MediaTVSeries, Title: req.Ref.NativeID, ExternalIDs: ids,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	for season := 1; season <= m.buildSeasons; season++ {
		container, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: req.Caller, ParentID: work.Work.ID, Kind: v1.NodeContainer,
			ContainerType: v1.ContainerSeason, Title: "Season " + itoaTest(season),
			NaturalOrder: float64(season),
		})
		if err != nil {
			return v1.ImportResult{}, err
		}
		for episode := 1; episode <= m.buildPerSeason; episode++ {
			if _, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
				Caller: req.Caller, ParentID: container.Node.ID, Kind: v1.NodeItem,
				ItemType: v1.ItemEpisode, Title: "Episode " + itoaTest(episode),
				NaturalOrder: float64(episode),
			}); err != nil {
				return v1.ImportResult{}, err
			}
		}
	}
	return v1.ImportResult{WorkID: work.Work.ID}, nil
}

func (m *seriesModule) Metadata(_ context.Context, req v1.MetadataRequest) (v1.ContentMetadata, error) {
	if m.failMetadata {
		return v1.ContentMetadata{}, contracts.NewError(contracts.Unavailable, "the source did not answer")
	}
	return v1.ContentMetadata{
		Ref: req.Ref, Title: req.Ref.NativeID, Overview: m.overview,
		Year: 2008, Rating: 8.9, Genres: []string{"Drama"},
		Cast:     []v1.Person{{Name: "Bryan Cranston", Role: "Walter White"}},
		Episodes: m.episodes,
	}, nil
}

func (m *seriesModule) Catalogs(context.Context, v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	return v1.CatalogsResponse{Catalogs: []v1.Catalog{{ID: "top", NativeType: "tv", Name: "Top"}}}, nil
}

func (m *seriesModule) CatalogItems(_ context.Context, req v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	if req.Skip > 0 {
		return v1.CatalogItemsResponse{}, nil
	}
	return v1.CatalogItemsResponse{Items: []v1.CatalogItem{{Ref: seriesRef(m.id), Title: "Breaking Bad"}}}, nil
}

func seriesRef(provider string) v1.ContentRef {
	return v1.ContentRef{
		Provider: provider, NativeID: "Breaking Bad", NativeType: "tv", MediaType: v1.MediaTVSeries,
		ExternalScheme: "imdb", ExternalID: "tt0903747",
	}
}

func episodesFor(seasons, per int) []v1.EpisodePreview {
	var out []v1.EpisodePreview
	for s := 1; s <= seasons; s++ {
		for e := 1; e <= per; e++ {
			out = append(out, v1.EpisodePreview{
				Season: s, Episode: e, Title: "S" + itoaTest(s) + "E" + itoaTest(e),
			})
		}
	}
	return out
}

func itoaTest(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// allEpisodes reads every season of a work, one season at a time, and projects
// the lot.
//
// It reads per season deliberately: that is how the detail screen reads, so a
// test asserting the tree grew is asserting it through the same path a viewer
// sees rather than through a whole-tree read nothing does any more.
func allEpisodes(t *testing.T, svc *app.Service, caller v1.Caller, workID v1.NodeID) []v1.EpisodePreview {
	t.Helper()
	first, err := svc.GetLibraryDetail(context.Background(), app.GetLibraryDetailQuery{
		Caller: caller, NodeID: workID,
	})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}
	var out []v1.EpisodePreview
	for _, season := range app.SeasonNumbers(first.Children) {
		d, err := svc.GetLibraryDetail(context.Background(), app.GetLibraryDetailQuery{
			Caller: caller, NodeID: workID, Season: season,
		})
		if err != nil {
			t.Fatalf("GetLibraryDetail(season %d): %v", season, err)
		}
		out = append(out, app.EpisodesFromTree(season, d.Episodes)...)
	}
	return out
}

// newSeriesService wires a Service over the fakes with one series module
// registered, and an administrator seeded as session "s-1".
func newSeriesService(t *testing.T, mod *seriesModule) *app.Service {
	t.Helper()
	now := time.Date(2026, 7, 27, 9, 0, 0, 0, time.UTC)
	db := newFakeDB()
	registry := app.NewCapabilityRegistry()
	registry.Register(mod)
	svc := newTestServiceWithCapabilities(db, &trace{}, now, registry)
	db.seedUser(domain.User{ID: "u-1", Username: "curator", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	return svc
}

func TestImportStoresWhatTheSourceSaid(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{
		id: "tmdb", buildSeasons: 1, buildPerSeason: 2,
		episodes: episodesFor(1, 2),
		overview: "A chemistry teacher turns to making meth.",
	}
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}

	detail, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{Caller: caller, NodeID: res.WorkID})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}
	if !detail.HasMetadata {
		t.Fatal("the import stored nothing, so a library detail has nothing to render")
	}
	if detail.Metadata.Overview != mod.overview {
		t.Fatalf("stored overview = %q, want the source's", detail.Metadata.Overview)
	}
	if len(detail.Metadata.Cast) != 1 {
		t.Fatalf("stored cast = %v, want the source's", detail.Metadata.Cast)
	}
	// **The document carries no episodes**: the tree is the authority, and a
	// second copy would disagree with it the moment the tree grew.
	if len(detail.Metadata.Episodes) != 0 {
		t.Fatalf("the stored document carries %d episodes; the tree is the authority", len(detail.Metadata.Episodes))
	}
	// And the tree is what the episode list is projected from.
	if got := len(allEpisodes(t, svc, caller, res.WorkID)); got != 2 {
		t.Fatalf("the tree projects %d episodes, want 2", got)
	}
}

// The failure ADR 0107 was written for: a source gains a season, and the
// household's copy has to grow.
func TestASeriesThatGainsASeasonGrows(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{id: "tmdb", buildSeasons: 1, buildPerSeason: 2, episodes: episodesFor(1, 2)}
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}
	workID := res.WorkID

	if got := len(allEpisodes(t, svc, caller, workID)); got != 2 {
		t.Fatalf("the first import produced %d episodes, want 2", got)
	}

	// The source gains a second season, and a third episode of the first.
	mod.episodes = []v1.EpisodePreview{
		{Season: 1, Episode: 1, Title: "S1E1"},
		{Season: 1, Episode: 2, Title: "S1E2"},
		{Season: 1, Episode: 3, Title: "S1E3"},
		{Season: 2, Episode: 1, Title: "S2E1"},
		{Season: 2, Episode: 2, Title: "S2E2"},
	}

	// A re-import, which is exactly what the maintenance pass does. The module
	// reports AlreadyKnown and builds nothing; the Platform's pass is what grows
	// the tree.
	again, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}
	if !again.AlreadyKnown {
		t.Fatal("the module did not report AlreadyKnown, so this is not the case under test")
	}

	grown := allEpisodes(t, svc, caller, workID)
	if len(grown) != 5 {
		t.Fatalf("the tree holds %d episodes after the source gained a season, want 5: %+v", len(grown), grown)
	}
	seasons := map[int]bool{}
	for _, e := range grown {
		seasons[e.Season] = true
	}
	if !seasons[2] {
		t.Fatal("season 2 was never created")
	}

	// **And it adds only what is missing.** A third run over an unchanged source
	// must not duplicate anything, which is what makes the pass safe to run on a
	// schedule.
	if _, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")}); err != nil {
		t.Fatalf("third ImportContent: %v", err)
	}
	if got := len(allEpisodes(t, svc, caller, workID)); got != 5 {
		t.Fatalf("a third run took the tree to %d episodes; it duplicated", got)
	}
}

// Rules add and never remove, and that holds one level down: an episode that
// leaves a source's listing stays, because somebody may be part-way through it.
func TestAnEpisodeThatLeavesTheSourceStays(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{id: "tmdb", buildSeasons: 1, buildPerSeason: 3, episodes: episodesFor(1, 3)}
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}

	mod.episodes = episodesFor(1, 1)
	if _, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")}); err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}

	if got := len(allEpisodes(t, svc, caller, res.WorkID)); got != 3 {
		t.Fatalf("the tree holds %d episodes after the source dropped two, want all 3 kept", got)
	}
}

// An episode the *module* created carries no still, because a module building a
// tree has none per episode. The pass fills the gap and never overrules a choice
// that was already made.
func TestTheTreeTopUpFillsAMissingEpisodeStill(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{id: "tmdb", buildSeasons: 1, buildPerSeason: 2}
	mod.episodes = []v1.EpisodePreview{
		{Season: 1, Episode: 1, Title: "Pilot", Thumbnail: "https://cdn/s1e1.jpg"},
		{Season: 1, Episode: 2, Title: "Cat's in the Bag...", Thumbnail: "https://cdn/s1e2.jpg"},
	}
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}
	for _, e := range allEpisodes(t, svc, caller, res.WorkID) {
		if e.Thumbnail == "" {
			t.Fatalf("S%dE%d has no still, so its row draws a grey rectangle", e.Season, e.Episode)
		}
	}

	// And a choice already made is left alone.
	detail, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{Caller: caller, NodeID: res.WorkID})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}
	var episode v1.Node
	for _, n := range detail.Episodes {
		if n.ItemType == v1.ItemEpisode {
			episode = n
			break
		}
	}
	if _, err := svc.SetContentArtwork(ctx, v1.SetContentArtworkCommand{
		Caller: caller, NodeID: episode.ID,
		Artwork: v1.Artwork{Landscape: "https://cdn/chosen.jpg"},
	}); err != nil {
		t.Fatalf("SetContentArtwork: %v", err)
	}
	if _, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")}); err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}
	after, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{Caller: caller, NodeID: res.WorkID})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}
	for _, n := range after.Episodes {
		if n.ID == episode.ID && n.Artwork.Landscape != "https://cdn/chosen.jpg" {
			t.Fatalf("the pass overruled a still that was already chosen: %q", n.Artwork.Landscape)
		}
	}
}

// A source that will not answer must leave the last document in place — that is
// a cache doing its job rather than failing.
func TestAProviderThatFailsLeavesTheStoredDocumentAlone(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{id: "tmdb", buildSeasons: 1, buildPerSeason: 1,
		episodes: episodesFor(1, 1), overview: "The first answer."}
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}

	// The source stops describing anything, as an unreachable one does.
	mod.overview = ""
	mod.failMetadata = true
	if _, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")}); err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}

	detail, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{Caller: caller, NodeID: res.WorkID})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}
	if detail.Metadata.Overview != "The first answer." {
		t.Fatalf("stored overview = %q; a failed read overwrote the cache", detail.Metadata.Overview)
	}
}

// A long-running series must not cost its whole tree to draw one season of it.
//
// This is the shape a daily programme takes in a real library: seventy-five
// seasons and twenty thousand episodes. Reading it whole to render seven rows
// took a second — not because PostgreSQL minds twenty thousand rows, but
// because sorting and marshalling them per render is paid for nothing.
func TestADetailReadsOneSeasonNotTheWholeSeries(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{id: "tmdb", buildSeasons: 40, buildPerSeason: 50}
	mod.episodes = episodesFor(40, 50)
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}

	detail, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{
		Caller: caller, NodeID: res.WorkID, Season: 7,
	})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}

	// Every season is offered, so the selector is complete...
	if got := len(app.SeasonNumbers(detail.Children)); got != 40 {
		t.Fatalf("the selector offers %d seasons, want all 40", got)
	}
	// ...and only one season's episodes were read.
	if len(detail.Episodes) != 50 {
		t.Fatalf("the read returned %d episodes, want the one season's 50", len(detail.Episodes))
	}
	if detail.Season != 7 {
		t.Fatalf("Season = %d, want the one that was asked for", detail.Season)
	}
	for _, e := range app.EpisodesFromTree(detail.Season, detail.Episodes) {
		if e.Season != 7 {
			t.Fatalf("an episode of season %d came back for a season-7 read", e.Season)
		}
	}
}

// Opening a season directly renders its series, because a season has no
// description of its own and nobody navigating to one wants less than they came
// from.
func TestOpeningASeasonRendersItsSeries(t *testing.T) {
	ctx := context.Background()
	mod := &seriesModule{id: "tmdb", buildSeasons: 2, buildPerSeason: 2,
		episodes: episodesFor(2, 2), overview: "Chemistry."}
	svc := newSeriesService(t, mod)
	caller := v1.Caller{Session: "s-1"}

	res, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: seriesRef("tmdb")})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}
	work, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{Caller: caller, NodeID: res.WorkID})
	if err != nil {
		t.Fatalf("GetLibraryDetail: %v", err)
	}

	season := work.Children[0]
	opened, err := svc.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{Caller: caller, NodeID: season.ID})
	if err != nil {
		t.Fatalf("GetLibraryDetail(season): %v", err)
	}
	if opened.Node.ID != res.WorkID {
		t.Fatalf("opening a season resolved to %q, want the series", opened.Node.ID)
	}
	if !opened.HasMetadata || opened.Metadata.Overview != "Chemistry." {
		t.Fatal("opening a season lost the series' description")
	}
}
