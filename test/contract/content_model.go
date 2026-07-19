package contract

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	v1 "github.com/mosaic-media/mosaic-platform/contracts/platform/v1"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// The content-model contract suite (ADR 0013). Like the rest of this package
// it imports only contracts and domain, so the same tests bind to any
// implementation of the object graph.

// contentID builds a deterministic, valid UUIDv7-shaped identifier. The
// content tables use native uuid columns, so test identifiers cannot be the
// free-form strings the infrastructure tables accept; making them a pure
// function of a seed keeps failures readable.
func contentID(kind string, seed int) string {
	// A fixed timestamp prefix, version 7, RFC 4122 variant, and the seed in
	// the trailing node bits.
	return fmt.Sprintf("0193c8f0-%s-7000-8000-%012x", kind, seed)
}

func nodeID(seed int) v1.NodeID    { return v1.NodeID(contentID("0d00", seed)) }
func partID(seed int) v1.PartID    { return v1.PartID(contentID("9a11", seed)) }
func relID(seed int) v1.RelationID { return v1.RelationID(contentID("13e1", seed)) }
func bindID(seed int) v1.SourceBindingID {
	return v1.SourceBindingID(contentID("b14d", seed))
}

// newWork builds a root Work. A Work is its own tree's work id and has no
// parent.
func newWork(id v1.NodeID, mediaType v1.MediaType, title string, at time.Time) v1.Node {
	return v1.Node{
		ID: id, WorkID: id, ParentID: nil,
		Kind: v1.NodeWork, MediaType: mediaType, Title: title,
		Status: v1.NodeActive, CreatedAt: at, UpdatedAt: at,
	}
}

func newContainer(id, work, parent v1.NodeID, mediaType v1.MediaType,
	containerType v1.ContainerType, title string, order float64, at time.Time) v1.Node {
	return v1.Node{
		ID: id, WorkID: work, ParentID: &parent,
		Kind: v1.NodeContainer, MediaType: mediaType, ContainerType: containerType,
		Title: title, NaturalOrder: order,
		Status: v1.NodeActive, CreatedAt: at, UpdatedAt: at,
	}
}

func newItem(id, work, parent v1.NodeID, mediaType v1.MediaType,
	itemType v1.ItemType, title string, order float64, at time.Time) v1.Node {
	return v1.Node{
		ID: id, WorkID: work, ParentID: &parent,
		Kind: v1.NodeItem, MediaType: mediaType, ItemType: itemType,
		Title: title, NaturalOrder: order,
		Status: v1.NodeActive, CreatedAt: at, UpdatedAt: at,
	}
}

func newPart(id v1.PartID, node v1.NodeID, role v1.PartRole,
	label string, order float64, at time.Time) v1.Part {
	return v1.Part{
		ID: id, NodeID: node, Role: role, EditionLabel: label, NaturalOrder: order,
		Location:  v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/media/" + string(id) + ".mkv"},
		Container: "matroska", VideoCodec: "hevc", AudioCodec: "eac3",
		Width: 3840, Height: 2160, HDRFormat: "dolby_vision",
		Duration: 2 * time.Hour, BitrateBPS: 42_000_000, SizeBytes: 38_000_000_000,
		CreatedAt: at, UpdatedAt: at,
	}
}

func titles(nodes []v1.Node) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Title
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// The published models carry no state-transition methods (ADR 0016), so the
// suite constructs transitions directly. These mirror what the resolve and
// orphan commands do internally.
func orphaned(n v1.Node, at time.Time) v1.Node {
	n.Status = v1.NodeOrphaned
	n.UpdatedAt = at
	return n
}

func confirmedBinding(b v1.SourceBinding, at time.Time) v1.SourceBinding {
	b.Status = v1.BindingConfirmed
	b.UpdatedAt = at
	return b
}

func movedBinding(b v1.SourceBinding, node v1.NodeID, at time.Time) v1.SourceBinding {
	b.NodeID = node
	b.UpdatedAt = at
	return b
}

// RunNodeStoreContract verifies the containment tree: variable depth,
// ordered sibling reads, fractional insertion, and refusal to cascade.
func RunNodeStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("create and find a work", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		created, err := d.Nodes.Create(c, newWork(nodeID(1), v1.MediaMovie, "Arrival", now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		found, err := d.Nodes.FindByID(c, created.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if found.Title != "Arrival" || found.Kind != v1.NodeWork {
			t.Fatalf("FindByID returned %+v", found)
		}
		if !found.IsRoot() {
			t.Fatal("a work must be a root")
		}
		if found.WorkID != found.ID {
			t.Fatalf("work id = %q, want its own id %q", found.WorkID, found.ID)
		}
	})

	t.Run("find missing is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Nodes.FindByID(ctx(t), nodeID(999))
		requireCategory(t, err, contracts.NotFound)
	})

	// Variable depth is the property that keeps every media type out of the
	// special-case pile: a film needs no container layer and must not be given
	// an empty one.
	t.Run("a film is work then item, with no container layer", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaMovie, "Arrival", now)
		if _, err := d.Nodes.Create(c, work); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		feature := newItem(nodeID(2), work.ID, work.ID, v1.MediaMovie, v1.ItemFeature, "Arrival", 1, now)
		if _, err := d.Nodes.Create(c, feature); err != nil {
			t.Fatalf("Create item: %v", err)
		}

		children, err := d.Nodes.ListChildren(c, work.ID)
		if err != nil {
			t.Fatalf("ListChildren: %v", err)
		}
		if len(children) != 1 || children[0].Kind != v1.NodeItem {
			t.Fatalf("a film's child should be one item, got %+v", children)
		}
	})

	t.Run("a series nests work, container and item", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Severance", now)
		season := newContainer(nodeID(2), work.ID, work.ID, v1.MediaTVSeries, v1.ContainerSeason, "Season 1", 1, now)
		for _, n := range []v1.Node{work, season} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create %s: %v", n.Title, err)
			}
		}
		for i, title := range []string{"Good News About Hell", "Half Loop", "In Perpetuity"} {
			ep := newItem(nodeID(10+i), work.ID, season.ID, v1.MediaTVSeries, v1.ItemEpisode, title, float64(i+1), now)
			if _, err := d.Nodes.Create(c, ep); err != nil {
				t.Fatalf("Create episode %s: %v", title, err)
			}
		}

		episodes, err := d.Nodes.ListChildren(c, season.ID)
		if err != nil {
			t.Fatalf("ListChildren: %v", err)
		}
		want := []string{"Good News About Hell", "Half Loop", "In Perpetuity"}
		if !equalStrings(titles(episodes), want) {
			t.Fatalf("episodes = %v, want %v", titles(episodes), want)
		}

		// The denormalised work id reaches the whole tree without a recursive
		// walk: the work, the season and three episodes.
		all, err := d.Nodes.ListByWork(c, work.ID)
		if err != nil {
			t.Fatalf("ListByWork: %v", err)
		}
		if len(all) != 5 {
			t.Fatalf("ListByWork returned %d nodes, want 5", len(all))
		}
	})

	// The float sort key exists so an insertion does not renumber its
	// siblings. This is the behaviour ADR 0013 specifies; the fractional
	// scheme at large scale is deliberately left open and nothing rebalances.
	t.Run("a fractional order inserts without renumbering siblings", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Show", now)
		if _, err := d.Nodes.Create(c, work); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		five := newItem(nodeID(5), work.ID, work.ID, v1.MediaTVSeries, v1.ItemEpisode, "Five", 5, now)
		six := newItem(nodeID(6), work.ID, work.ID, v1.MediaTVSeries, v1.ItemEpisode, "Six", 6, now)
		for _, n := range []v1.Node{five, six} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}

		// A recap episode arrives between them.
		recap := newItem(nodeID(7), work.ID, work.ID, v1.MediaTVSeries, v1.ItemEpisode, "Five and a half", 5.5, now)
		if _, err := d.Nodes.Create(c, recap); err != nil {
			t.Fatalf("Create recap: %v", err)
		}

		children, err := d.Nodes.ListChildren(c, work.ID)
		if err != nil {
			t.Fatalf("ListChildren: %v", err)
		}
		if !equalStrings(titles(children), []string{"Five", "Five and a half", "Six"}) {
			t.Fatalf("order = %v", titles(children))
		}
		// The neighbours kept the numbers they were created with.
		if children[0].NaturalOrder != 5 || children[2].NaturalOrder != 6 {
			t.Fatalf("siblings were renumbered: %v, %v", children[0].NaturalOrder, children[2].NaturalOrder)
		}
	})

	t.Run("list works returns roots and filters by media type", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		film := newWork(nodeID(1), v1.MediaMovie, "Arrival", now)
		series := newWork(nodeID(2), v1.MediaTVSeries, "Severance", now)
		for _, n := range []v1.Node{film, series} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		// A child must not surface as a work.
		child := newItem(nodeID(3), film.ID, film.ID, v1.MediaMovie, v1.ItemFeature, "Arrival Feature", 1, now)
		if _, err := d.Nodes.Create(c, child); err != nil {
			t.Fatalf("Create child: %v", err)
		}

		all, err := d.Nodes.ListWorks(c, "")
		if err != nil {
			t.Fatalf("ListWorks: %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("ListWorks(\"\") returned %d, want 2 roots: %v", len(all), titles(all))
		}
		films, err := d.Nodes.ListWorks(c, v1.MediaMovie)
		if err != nil {
			t.Fatalf("ListWorks(movie): %v", err)
		}
		if len(films) != 1 || films[0].Title != "Arrival" {
			t.Fatalf("ListWorks(movie) = %v", titles(films))
		}
	})

	// The open vocabularies are unconstrained text (ADR 0015), so spelling
	// variants of one concept must converge on write or a library silently
	// splits in two. Any implementation of the contract owes this.
	t.Run("type vocabularies are stored canonically", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		// Three capabilities, three spellings, one media type.
		for i, spelling := range []string{"Anime Series", "anime-series", "anime_series"} {
			work := newWork(nodeID(i+1), v1.MediaType(spelling), fmt.Sprintf("Show %d", i+1), now)
			created, err := d.Nodes.Create(c, work)
			if err != nil {
				t.Fatalf("Create %q: %v", spelling, err)
			}
			// The write returns what was actually stored.
			if created.MediaType != v1.MediaAnimeSeries {
				t.Fatalf("Create(%q) returned media type %q, want %q",
					spelling, created.MediaType, v1.MediaAnimeSeries)
			}
			found, err := d.Nodes.FindByID(c, created.ID)
			if err != nil {
				t.Fatalf("FindByID: %v", err)
			}
			if found.MediaType != v1.MediaAnimeSeries {
				t.Fatalf("stored %q as %q, want %q", spelling, found.MediaType, v1.MediaAnimeSeries)
			}
		}

		// All three browse as one library rather than three.
		works, err := d.Nodes.ListWorks(c, v1.MediaAnimeSeries)
		if err != nil {
			t.Fatalf("ListWorks: %v", err)
		}
		if len(works) != 3 {
			t.Fatalf("ListWorks returned %d works, want all 3 spellings in one bucket", len(works))
		}

		// And a filter written the long way finds them too.
		byVariant, err := d.Nodes.ListWorks(c, "Anime Series")
		if err != nil {
			t.Fatalf("ListWorks(variant): %v", err)
		}
		if len(byVariant) != 3 {
			t.Fatalf("filtering by %q returned %d works, want 3", "Anime Series", len(byVariant))
		}
	})

	t.Run("container and item types are canonical too", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Show", now)
		if _, err := d.Nodes.Create(c, work); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		season := newContainer(nodeID(2), work.ID, work.ID, v1.MediaTVSeries, "Box-Set", "Set", 1, now)
		created, err := d.Nodes.Create(c, season)
		if err != nil {
			t.Fatalf("Create container: %v", err)
		}
		if created.ContainerType != v1.ContainerBoxSet {
			t.Fatalf("container type = %q, want %q", created.ContainerType, v1.ContainerBoxSet)
		}
		item := newItem(nodeID(3), work.ID, season.ID, v1.MediaTVSeries, "Special Feature", "Extra", 1, now)
		createdItem, err := d.Nodes.Create(c, item)
		if err != nil {
			t.Fatalf("Create item: %v", err)
		}
		if createdItem.ItemType != "special_feature" {
			t.Fatalf("item type = %q, want special_feature", createdItem.ItemType)
		}
	})

	// "Do I already have this?" is the read a capability makes before it
	// sources anything, and a browse surface makes for a user typing.
	t.Run("search narrows by title, media type and kind", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		anime := newWork(nodeID(1), v1.MediaAnimeSeries, "Fullmetal Alchemist: Brotherhood", now)
		manga := newWork(nodeID(2), v1.MediaMangaSeries, "Fullmetal Alchemist", now)
		other := newWork(nodeID(3), v1.MediaMovie, "Arrival", now)
		for _, n := range []v1.Node{anime, manga, other} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create %s: %v", n.Title, err)
			}
		}
		episode := newItem(nodeID(4), anime.ID, anime.ID, v1.MediaAnimeSeries, v1.ItemEpisode, "Fullmetal Alchemist", 1, now)
		if _, err := d.Nodes.Create(c, episode); err != nil {
			t.Fatalf("Create episode: %v", err)
		}

		// Substring, not prefix: "alchemist" must find "Fullmetal Alchemist".
		byTitle, err := d.Nodes.Search(c, contracts.NodeQuery{Title: "alchemist", Limit: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(byTitle) != 3 {
			t.Fatalf("title search found %v, want the two works and the episode", titles(byTitle))
		}

		// Kind narrows to works — "does this show exist", not "does some
		// episode exist".
		works, err := d.Nodes.Search(c, contracts.NodeQuery{Title: "alchemist", Kind: v1.NodeWork, Limit: 10})
		if err != nil {
			t.Fatalf("Search works: %v", err)
		}
		if len(works) != 2 {
			t.Fatalf("work search found %v, want 2", titles(works))
		}

		// Media type separates the anime from its source manga.
		animeOnly, err := d.Nodes.Search(c, contracts.NodeQuery{
			Title: "alchemist", Kind: v1.NodeWork, MediaType: v1.MediaAnimeSeries, Limit: 10,
		})
		if err != nil {
			t.Fatalf("Search anime: %v", err)
		}
		if len(animeOnly) != 1 || animeOnly[0].ID != anime.ID {
			t.Fatalf("anime search = %v", titles(animeOnly))
		}

		// The empty query is "everything", not "nothing".
		all, err := d.Nodes.Search(c, contracts.NodeQuery{Limit: 10})
		if err != nil {
			t.Fatalf("Search all: %v", err)
		}
		if len(all) != 4 {
			t.Fatalf("empty query found %d nodes, want all 4", len(all))
		}
	})

	t.Run("search is case-insensitive and honours its limit", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		for i, title := range []string{"Akira", "Akira Reborn", "Akira Legacy"} {
			if _, err := d.Nodes.Create(c, newWork(nodeID(i+1), v1.MediaMovie, title, now)); err != nil {
				t.Fatalf("Create %s: %v", title, err)
			}
		}
		found, err := d.Nodes.Search(c, contracts.NodeQuery{Title: "AKIRA", Limit: 2})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(found) != 2 {
			t.Fatalf("limit not honoured: got %d, want 2", len(found))
		}
	})

	// A user's library is not a place to run an unbounded scan from.
	t.Run("a non-positive search limit is rejected", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Nodes.Search(ctx(t), contracts.NodeQuery{Limit: 0})
		requireCategory(t, err, contracts.InvalidArgument)
	})

	// Wildcards in a title are searched for literally, not as a pattern —
	// otherwise a title containing % matches everything.
	t.Run("search treats wildcards as literal text", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		if _, err := d.Nodes.Create(c, newWork(nodeID(1), v1.MediaMovie, "100% Wolf", now)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := d.Nodes.Create(c, newWork(nodeID(2), v1.MediaMovie, "Arrival", now)); err != nil {
			t.Fatalf("Create: %v", err)
		}
		found, err := d.Nodes.Search(c, contracts.NodeQuery{Title: "100%", Limit: 10})
		if err != nil {
			t.Fatalf("Search: %v", err)
		}
		if len(found) != 1 || found[0].Title != "100% Wolf" {
			t.Fatalf("wildcard search = %v, want only the literal match", titles(found))
		}
	})

	// The strong form of "do I already have this": it does not depend on
	// titles matching, which is why a metadata capability reaches for it first.
	t.Run("nodes are findable by a provider identifier", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		anime := newWork(nodeID(1), v1.MediaAnimeSeries, "Fullmetal Alchemist: Brotherhood", now)
		anime.ExternalIDs = []byte(`{"anilist":"5114","mal":"5114"}`)
		manga := newWork(nodeID(2), v1.MediaMangaSeries, "Fullmetal Alchemist", now)
		manga.ExternalIDs = []byte(`{"anilist":"30002"}`)
		unrelated := newWork(nodeID(3), v1.MediaMovie, "Arrival", now)
		unrelated.ExternalIDs = []byte(`{"tmdb":"329865"}`)
		for _, n := range []v1.Node{anime, manga, unrelated} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create %s: %v", n.Title, err)
			}
		}

		found, err := d.Nodes.FindByExternalID(c, "anilist", "5114")
		if err != nil {
			t.Fatalf("FindByExternalID: %v", err)
		}
		if len(found) != 1 || found[0].ID != anime.ID {
			t.Fatalf("lookup = %v, want the anime alone", titles(found))
		}

		// The same value under a different scheme is a different identifier.
		none, err := d.Nodes.FindByExternalID(c, "tmdb", "5114")
		if err != nil {
			t.Fatalf("FindByExternalID: %v", err)
		}
		if len(none) != 0 {
			t.Fatalf("scheme is not being matched: got %v", titles(none))
		}

		// An unknown id is an empty result, not an error — "no" is an answer.
		missing, err := d.Nodes.FindByExternalID(c, "anilist", "999999")
		if err != nil {
			t.Fatalf("FindByExternalID: %v", err)
		}
		if len(missing) != 0 {
			t.Fatalf("expected no matches, got %v", titles(missing))
		}
	})

	t.Run("an empty external id lookup is rejected", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		_, err := d.Nodes.FindByExternalID(c, "", "5114")
		requireCategory(t, err, contracts.InvalidArgument)
		_, err = d.Nodes.FindByExternalID(c, "anilist", "")
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("update changes fields", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work, err := d.Nodes.Create(c, newWork(nodeID(1), v1.MediaMovie, "Untitled", now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		work.Title = "Arrival"
		work.Attributes = []byte(`{"year":2016}`)
		work.UpdatedAt = now.Add(time.Hour)
		if _, err := d.Nodes.Update(c, work); err != nil {
			t.Fatalf("Update: %v", err)
		}
		found, err := d.Nodes.FindByID(c, work.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if found.Title != "Arrival" {
			t.Fatalf("title = %q", found.Title)
		}
	})

	t.Run("orphaning is a status change, not a deletion", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work, err := d.Nodes.Create(c, newWork(nodeID(1), v1.MediaMovie, "Arrival", now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if _, err := d.Nodes.Update(c, orphaned(work, now.Add(time.Hour))); err != nil {
			t.Fatalf("Update: %v", err)
		}
		found, err := d.Nodes.FindByID(c, work.ID)
		if err != nil {
			t.Fatalf("an orphaned node must still exist: %v", err)
		}
		if !found.Orphaned() {
			t.Fatalf("status = %q, want orphaned", found.Status)
		}
	})

	// Deletion is a decision a user confirms, never a silent cascade.
	t.Run("deleting a node with children is refused", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Severance", now)
		season := newContainer(nodeID(2), work.ID, work.ID, v1.MediaTVSeries, v1.ContainerSeason, "Season 1", 1, now)
		for _, n := range []v1.Node{work, season} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		requireCategory(t, d.Nodes.Delete(c, work.ID), contracts.Conflict)

		// The subtree is untouched.
		if _, err := d.Nodes.FindByID(c, season.ID); err != nil {
			t.Fatalf("the child must survive a refused delete: %v", err)
		}
		// Depth-first deletion works.
		if err := d.Nodes.Delete(c, season.ID); err != nil {
			t.Fatalf("Delete leaf: %v", err)
		}
		if err := d.Nodes.Delete(c, work.ID); err != nil {
			t.Fatalf("Delete work after its child: %v", err)
		}
	})

	t.Run("delete missing is not found", func(t *testing.T) {
		d := newDeps(t)
		requireCategory(t, d.Nodes.Delete(ctx(t), nodeID(999)), contracts.NotFound)
	})

	// container_type and item_type are set only for their respective kinds;
	// the schema, not convention, is what holds that.
	t.Run("a container type on a work is rejected", func(t *testing.T) {
		d := newDeps(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Severance", now)
		work.ContainerType = v1.ContainerSeason
		_, err := d.Nodes.Create(ctx(t), work)
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("an item type on a container is rejected", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Severance", now)
		if _, err := d.Nodes.Create(c, work); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		season := newContainer(nodeID(2), work.ID, work.ID, v1.MediaTVSeries, v1.ContainerSeason, "Season 1", 1, now)
		season.ItemType = v1.ItemEpisode
		_, err := d.Nodes.Create(c, season)
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("a work with a parent is rejected", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		root := newWork(nodeID(1), v1.MediaTVSeries, "Severance", now)
		if _, err := d.Nodes.Create(c, root); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		nested := newWork(nodeID(2), v1.MediaTVSeries, "Nested", now)
		nested.ParentID = &root.ID
		_, err := d.Nodes.Create(c, nested)
		requireCategory(t, err, contracts.InvalidArgument)
	})
}

// RunPartStoreContract verifies that Parts carry bytes for item nodes, that
// editions and segments share one selection path, and that local and remote
// locations are equally first-class.
func RunPartStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	// seedItem creates a work and one item beneath it, returning the item.
	seedItem := func(t *testing.T, d Deps, c context.Context) v1.Node {
		t.Helper()
		work := newWork(nodeID(1), v1.MediaMovie, "Blade Runner 2049", now)
		if _, err := d.Nodes.Create(c, work); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		item := newItem(nodeID(2), work.ID, work.ID, v1.MediaMovie, v1.ItemFeature, "Blade Runner 2049", 1, now)
		if _, err := d.Nodes.Create(c, item); err != nil {
			t.Fatalf("Create item: %v", err)
		}
		return item
	}

	t.Run("create and find", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		item := seedItem(t, d, c)
		created, err := d.Parts.Create(c, newPart(partID(1), item.ID, v1.PartEdition, "", 1, now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		found, err := d.Parts.FindByID(c, created.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if found.NodeID != item.ID || found.Container != "matroska" || found.Duration != 2*time.Hour {
			t.Fatalf("FindByID returned %+v", found)
		}
	})

	t.Run("find missing is not found", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Parts.FindByID(ctx(t), partID(999))
		requireCategory(t, err, contracts.NotFound)
	})

	// An edition or cut is NOT a new Node. One Item carries however many cuts
	// exist, because the cut is a property of which bytes play.
	t.Run("editions are parts of one item, not separate nodes", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		item := seedItem(t, d, c)

		for i, label := range []string{"Theatrical", "Director's Cut", "Final Cut"} {
			p := newPart(partID(10+i), item.ID, v1.PartEdition, label, float64(i+1), now)
			if _, err := d.Parts.Create(c, p); err != nil {
				t.Fatalf("Create edition %s: %v", label, err)
			}
		}

		parts, err := d.Parts.ListByNode(c, item.ID)
		if err != nil {
			t.Fatalf("ListByNode: %v", err)
		}
		if len(parts) != 3 {
			t.Fatalf("got %d parts, want 3 editions on one item", len(parts))
		}
		// And the tree did not grow: the work still holds exactly one item.
		tree, err := d.Nodes.ListByWork(c, item.WorkID)
		if err != nil {
			t.Fatalf("ListByWork: %v", err)
		}
		if len(tree) != 2 {
			t.Fatalf("three editions produced %d nodes; editions must not become nodes", len(tree))
		}
	})

	t.Run("segments of a multi-disc release stay in order", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		item := seedItem(t, d, c)
		for i, disc := range []string{"Disc 1", "Disc 2", "Disc 3"} {
			p := newPart(partID(20+i), item.ID, v1.PartSegment, disc, float64(i+1), now)
			if _, err := d.Parts.Create(c, p); err != nil {
				t.Fatalf("Create %s: %v", disc, err)
			}
		}
		parts, err := d.Parts.ListByNode(c, item.ID)
		if err != nil {
			t.Fatalf("ListByNode: %v", err)
		}
		for i, want := range []string{"Disc 1", "Disc 2", "Disc 3"} {
			if parts[i].EditionLabel != want {
				t.Fatalf("segment %d = %q, want %q", i, parts[i].EditionLabel, want)
			}
		}
	})

	// A Part points at bytes and never contains them. Local and remote are
	// both first-class: nothing above the Part cares which.
	t.Run("local and remote locations are both first-class", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		item := seedItem(t, d, c)

		local := newPart(partID(1), item.ID, v1.PartEdition, "Local", 1, now)
		local.Location = v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/mnt/media/br2049.mkv"}
		remote := newPart(partID(2), item.ID, v1.PartEdition, "Remote", 2, now)
		remote.Location = v1.MediaLocation{
			Scheme: v1.RemoteLocation, Provider: "debrid", Ref: "magnet:?xt=urn:btih:abcdef",
		}
		for _, p := range []v1.Part{local, remote} {
			if _, err := d.Parts.Create(c, p); err != nil {
				t.Fatalf("Create %s: %v", p.EditionLabel, err)
			}
		}

		parts, err := d.Parts.ListByNode(c, item.ID)
		if err != nil {
			t.Fatalf("ListByNode: %v", err)
		}
		if len(parts) != 2 {
			t.Fatalf("got %d parts, want 2", len(parts))
		}
		if !parts[0].Local() {
			t.Fatal("first part should be local")
		}
		if parts[1].Local() || parts[1].Location.Provider != "debrid" {
			t.Fatalf("second part should be a remote reference, got %+v", parts[1].Location)
		}
	})

	// A Part is what gets played, and a work or container has nothing to play.
	t.Run("a part cannot attach to a work or a container", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := newWork(nodeID(1), v1.MediaTVSeries, "Severance", now)
		season := newContainer(nodeID(2), work.ID, work.ID, v1.MediaTVSeries, v1.ContainerSeason, "Season 1", 1, now)
		for _, n := range []v1.Node{work, season} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create: %v", err)
			}
		}
		_, err := d.Parts.Create(c, newPart(partID(1), work.ID, v1.PartEdition, "", 1, now))
		requireCategory(t, err, contracts.InvalidArgument)

		_, err = d.Parts.Create(c, newPart(partID(2), season.ID, v1.PartEdition, "", 1, now))
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("a part cannot attach to a node that does not exist", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.Parts.Create(ctx(t), newPart(partID(1), nodeID(999), v1.PartEdition, "", 1, now))
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("update and delete", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		item := seedItem(t, d, c)
		part, err := d.Parts.Create(c, newPart(partID(1), item.ID, v1.PartEdition, "", 1, now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		part.EditionLabel = "Final Cut"
		part.UpdatedAt = now.Add(time.Hour)
		if _, err := d.Parts.Update(c, part); err != nil {
			t.Fatalf("Update: %v", err)
		}
		found, err := d.Parts.FindByID(c, part.ID)
		if err != nil {
			t.Fatalf("FindByID: %v", err)
		}
		if found.EditionLabel != "Final Cut" {
			t.Fatalf("edition label = %q", found.EditionLabel)
		}
		if err := d.Parts.Delete(c, part.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		requireCategory(t, d.Parts.Delete(c, part.ID), contracts.NotFound)
	})

	// Bytes behind a node are a reason not to delete it silently.
	t.Run("deleting an item with parts is refused", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		item := seedItem(t, d, c)
		if _, err := d.Parts.Create(c, newPart(partID(1), item.ID, v1.PartEdition, "", 1, now)); err != nil {
			t.Fatalf("Create part: %v", err)
		}
		requireCategory(t, d.Nodes.Delete(c, item.ID), contracts.Conflict)
	})
}

// RunRelationStoreContract verifies the association graph: typed directed
// edges, both directions indexed, and no mutation of a written edge.
func RunRelationStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	seedTwoWorks := func(t *testing.T, d Deps, c context.Context) (v1.Node, v1.Node) {
		t.Helper()
		a := newWork(nodeID(1), v1.MediaAnimeSeries, "Fullmetal Alchemist: Brotherhood", now)
		b := newWork(nodeID(2), v1.MediaMangaSeries, "Fullmetal Alchemist", now)
		for _, n := range []v1.Node{a, b} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create %s: %v", n.Title, err)
			}
		}
		return a, b
	}

	t.Run("create and read an edge in both directions", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		anime, manga := seedTwoWorks(t, d, c)

		edge := v1.Relation{
			ID: relID(1), FromNodeID: anime.ID, ToNodeID: manga.ID,
			Type: v1.RelationAdaptation, Confidence: 0.98,
			Origin: v1.OriginProviderSupplied, CreatedAt: now,
		}
		if _, err := d.Relations.Create(c, edge); err != nil {
			t.Fatalf("Create: %v", err)
		}

		out, err := d.Relations.ListFrom(c, anime.ID, "")
		if err != nil {
			t.Fatalf("ListFrom: %v", err)
		}
		if len(out) != 1 || out[0].ToNodeID != manga.ID || out[0].Confidence != 0.98 {
			t.Fatalf("ListFrom = %+v", out)
		}

		in, err := d.Relations.ListTo(c, manga.ID, "")
		if err != nil {
			t.Fatalf("ListTo: %v", err)
		}
		if len(in) != 1 || in[0].FromNodeID != anime.ID {
			t.Fatalf("ListTo = %+v", in)
		}
		if in[0].Origin != v1.OriginProviderSupplied {
			t.Fatalf("origin = %q", in[0].Origin)
		}
	})

	t.Run("list filters by relation type", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		a, b := seedTwoWorks(t, d, c)
		for i, rt := range []v1.RelationType{v1.RelationAdaptation, v1.RelationSameFranchise} {
			edge := v1.Relation{
				ID: relID(i + 1), FromNodeID: a.ID, ToNodeID: b.ID, Type: rt,
				Confidence: 0.5, Origin: v1.OriginSystemInferred, CreatedAt: now,
			}
			if _, err := d.Relations.Create(c, edge); err != nil {
				t.Fatalf("Create %s: %v", rt, err)
			}
		}
		all, err := d.Relations.ListFrom(c, a.ID, "")
		if err != nil {
			t.Fatalf("ListFrom(all): %v", err)
		}
		if len(all) != 2 {
			t.Fatalf("got %d edges, want 2", len(all))
		}
		adaptations, err := d.Relations.ListFrom(c, a.ID, v1.RelationAdaptation)
		if err != nil {
			t.Fatalf("ListFrom(adaptation): %v", err)
		}
		if len(adaptations) != 1 || adaptations[0].Type != v1.RelationAdaptation {
			t.Fatalf("filtered = %+v", adaptations)
		}
	})

	t.Run("a duplicate edge is a conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		a, b := seedTwoWorks(t, d, c)
		edge := v1.Relation{
			ID: relID(1), FromNodeID: a.ID, ToNodeID: b.ID, Type: v1.RelationAdaptation,
			Confidence: 0.9, Origin: v1.OriginSystemInferred, CreatedAt: now,
		}
		if _, err := d.Relations.Create(c, edge); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		edge.ID = relID(2)
		_, err := d.Relations.Create(c, edge)
		requireCategory(t, err, contracts.Conflict)
	})

	t.Run("a self loop is rejected", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		a, _ := seedTwoWorks(t, d, c)
		_, err := d.Relations.Create(c, v1.Relation{
			ID: relID(1), FromNodeID: a.ID, ToNodeID: a.ID, Type: v1.RelationSequel,
			Confidence: 1, Origin: v1.OriginUserConfirmed, CreatedAt: now,
		})
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("confidence outside zero to one is rejected", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		a, b := seedTwoWorks(t, d, c)
		_, err := d.Relations.Create(c, v1.Relation{
			ID: relID(1), FromNodeID: a.ID, ToNodeID: b.ID, Type: v1.RelationAdaptation,
			Confidence: 1.5, Origin: v1.OriginSystemInferred, CreatedAt: now,
		})
		requireCategory(t, err, contracts.InvalidArgument)
	})

	t.Run("find missing and delete", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		_, err := d.Relations.FindByID(c, relID(999))
		requireCategory(t, err, contracts.NotFound)

		a, b := seedTwoWorks(t, d, c)
		edge, err := d.Relations.Create(c, v1.Relation{
			ID: relID(1), FromNodeID: a.ID, ToNodeID: b.ID, Type: v1.RelationAdaptation,
			Confidence: 0.9, Origin: v1.OriginSystemInferred, CreatedAt: now,
		})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := d.Relations.Delete(c, edge.ID); err != nil {
			t.Fatalf("Delete: %v", err)
		}
		requireCategory(t, d.Relations.Delete(c, edge.ID), contracts.NotFound)
	})
}

// RunSourceBindingStoreContract verifies that identity resolution is explicit
// — that weak matches queue rather than merging, and that a split moves a
// binding without re-resolving it.
func RunSourceBindingStoreContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	newBinding := func(id v1.SourceBindingID, node v1.NodeID, ref string,
		confidence float64, method v1.MatchMethod, status v1.BindingStatus, at time.Time) v1.SourceBinding {
		return v1.SourceBinding{
			ID: id, NodeID: node, SourceProvider: "tmdb", SourceRef: ref,
			MatchConfidence: confidence, MatchMethod: method, Status: status,
			CreatedAt: at, UpdatedAt: at,
		}
	}

	seedWork := func(t *testing.T, d Deps, c context.Context, seed int, title string) v1.Node {
		t.Helper()
		work := newWork(nodeID(seed), v1.MediaMovie, title, now)
		if _, err := d.Nodes.Create(c, work); err != nil {
			t.Fatalf("Create work: %v", err)
		}
		return work
	}

	t.Run("create and find by source", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := seedWork(t, d, c, 1, "Arrival")
		created, err := d.SourceBindings.Create(c,
			newBinding(bindID(1), work.ID, "329865", 1, v1.MatchExternalIDExact, v1.BindingConfirmed, now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		found, err := d.SourceBindings.FindBySource(c, "tmdb", "329865")
		if err != nil {
			t.Fatalf("FindBySource: %v", err)
		}
		if found.ID != created.ID || found.NodeID != work.ID {
			t.Fatalf("FindBySource = %+v", found)
		}
	})

	t.Run("find missing is not found", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		_, err := d.SourceBindings.FindByID(c, bindID(999))
		requireCategory(t, err, contracts.NotFound)
		_, err = d.SourceBindings.FindBySource(c, "tmdb", "nope")
		requireCategory(t, err, contracts.NotFound)
	})

	// One source binds to at most one node. That is what makes a split a move
	// rather than a copy.
	t.Run("binding one source twice is a conflict", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		a := seedWork(t, d, c, 1, "Arrival")
		b := seedWork(t, d, c, 2, "Arrival (1990)")
		if _, err := d.SourceBindings.Create(c,
			newBinding(bindID(1), a.ID, "329865", 1, v1.MatchExternalIDExact, v1.BindingConfirmed, now)); err != nil {
			t.Fatalf("first Create: %v", err)
		}
		_, err := d.SourceBindings.Create(c,
			newBinding(bindID(2), b.ID, "329865", 1, v1.MatchExternalIDExact, v1.BindingConfirmed, now))
		requireCategory(t, err, contracts.Conflict)
	})

	// A weak match surfaces to the user rather than silently merging two
	// different works that happen to share a title.
	t.Run("a weak match queues for review instead of merging", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := seedWork(t, d, c, 1, "The Thing")
		weak := newBinding(bindID(1), work.ID, "thing-1982", 0.42, v1.MatchFuzzyTitle, v1.BindingPendingReview, now)
		if _, err := d.SourceBindings.Create(c, weak); err != nil {
			t.Fatalf("Create: %v", err)
		}
		strong := newBinding(bindID(2), work.ID, "thing-2011", 0.99, v1.MatchExternalIDExact, v1.BindingConfirmed, now)
		if _, err := d.SourceBindings.Create(c, strong); err != nil {
			t.Fatalf("Create confirmed: %v", err)
		}

		pending, err := d.SourceBindings.ListPendingReview(c, 10)
		if err != nil {
			t.Fatalf("ListPendingReview: %v", err)
		}
		if len(pending) != 1 || pending[0].ID != weak.ID {
			t.Fatalf("review queue = %+v, want only the weak match", pending)
		}
		if !pending[0].NeedsReview() {
			t.Fatal("queued binding should report NeedsReview")
		}

		// Confirming it — a merge — takes it off the queue.
		if _, err := d.SourceBindings.Update(c, confirmedBinding(pending[0], now.Add(time.Hour))); err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		pending, err = d.SourceBindings.ListPendingReview(c, 10)
		if err != nil {
			t.Fatalf("ListPendingReview after confirm: %v", err)
		}
		if len(pending) != 0 {
			t.Fatalf("review queue = %+v, want empty", pending)
		}
	})

	t.Run("a non-positive limit is rejected", func(t *testing.T) {
		d := newDeps(t)
		_, err := d.SourceBindings.ListPendingReview(ctx(t), 0)
		requireCategory(t, err, contracts.InvalidArgument)
	})

	// A split moves a binding to a different node. The source is never
	// re-fingerprinted and nothing else in the graph needs to know.
	t.Run("a split moves a binding without re-resolving it", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		wrong := seedWork(t, d, c, 1, "The Thing")
		right := seedWork(t, d, c, 2, "The Thing (2011)")
		binding, err := d.SourceBindings.Create(c,
			newBinding(bindID(1), wrong.ID, "thing-2011", 0.61, v1.MatchFingerprint, v1.BindingPendingReview, now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		moved := confirmedBinding(movedBinding(binding, right.ID, now.Add(time.Hour)), now.Add(time.Hour))
		if _, err := d.SourceBindings.Update(c, moved); err != nil {
			t.Fatalf("Update: %v", err)
		}

		found, err := d.SourceBindings.FindBySource(c, "tmdb", "thing-2011")
		if err != nil {
			t.Fatalf("FindBySource: %v", err)
		}
		if found.NodeID != right.ID {
			t.Fatalf("binding node = %q, want %q", found.NodeID, right.ID)
		}
		// Identity survived the move untouched.
		if found.MatchMethod != v1.MatchFingerprint || found.MatchConfidence != 0.61 {
			t.Fatalf("a split must not re-resolve identity, got %+v", found)
		}
		// And the old node kept nothing.
		left, err := d.SourceBindings.ListByNode(c, wrong.ID)
		if err != nil {
			t.Fatalf("ListByNode: %v", err)
		}
		if len(left) != 0 {
			t.Fatalf("old node still holds %d bindings", len(left))
		}
	})

	// Removing the last binding leaves the node standing. Deleting it is a
	// separate, confirmed decision.
	t.Run("removing the last binding orphans rather than deletes", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)
		work := seedWork(t, d, c, 1, "Arrival")
		binding, err := d.SourceBindings.Create(c,
			newBinding(bindID(1), work.ID, "329865", 1, v1.MatchExternalIDExact, v1.BindingConfirmed, now))
		if err != nil {
			t.Fatalf("Create: %v", err)
		}

		// A node with a source behind it is not deletable out from under it.
		requireCategory(t, d.Nodes.Delete(c, work.ID), contracts.Conflict)

		if err := d.SourceBindings.Delete(c, binding.ID); err != nil {
			t.Fatalf("Delete binding: %v", err)
		}
		remaining, err := d.SourceBindings.ListByNode(c, work.ID)
		if err != nil {
			t.Fatalf("ListByNode: %v", err)
		}
		if len(remaining) != 0 {
			t.Fatalf("expected no bindings, got %d", len(remaining))
		}

		// The node survives, and it is the caller that marks it orphaned.
		if _, err := d.Nodes.Update(c, orphaned(work, now.Add(time.Hour))); err != nil {
			t.Fatalf("MarkOrphaned: %v", err)
		}
		found, err := d.Nodes.FindByID(c, work.ID)
		if err != nil {
			t.Fatalf("the node must survive losing its last binding: %v", err)
		}
		if !found.Orphaned() {
			t.Fatalf("status = %q, want orphaned", found.Status)
		}
	})
}

// RunContentNonUniformityContract pins ADR 0013's four deliberate
// non-uniformities.
//
// Forcing every media type through one shape is its own bug, so four cases are
// modelled against the grain on purpose. Each is cheap to normalise away by
// accident — by making an artist a parent, by folding a collected edition into
// what it collects, by merging an anime with its source manga, or by giving
// programme listings the full Node machinery — and each of these tests fails
// if that happens.
func RunContentNonUniformityContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	// Box sets, collaborations and various-artist compilations all break
	// single-parent containment, so an artist is its own Work joined to album
	// Works by Relation — never their parent.
	t.Run("an artist is a work related to albums, not their container", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		artist := newWork(nodeID(1), "artist", "Radiohead", now)
		soloAlbum := newWork(nodeID(2), v1.MediaAlbum, "In Rainbows", now)
		// A collaboration: two artists, one album. This is the case that makes
		// containment impossible.
		other := newWork(nodeID(3), "artist", "Burial", now)
		collab := newWork(nodeID(4), v1.MediaAlbum, "Ego / Mirror", now)
		for _, n := range []v1.Node{artist, soloAlbum, other, collab} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create %s: %v", n.Title, err)
			}
		}

		// Every album is a root in its own right.
		works, err := d.Nodes.ListWorks(c, v1.MediaAlbum)
		if err != nil {
			t.Fatalf("ListWorks: %v", err)
		}
		if len(works) != 2 {
			t.Fatalf("albums = %v, want both as independent works", titles(works))
		}
		// An artist contains nothing.
		children, err := d.Nodes.ListChildren(c, artist.ID)
		if err != nil {
			t.Fatalf("ListChildren: %v", err)
		}
		if len(children) != 0 {
			t.Fatalf("an artist must not contain albums, got %v", titles(children))
		}

		// The association is edges, and the collaboration carries two of them
		// pointing at one album — which single-parent containment cannot do.
		edges := []v1.Relation{
			{ID: relID(1), FromNodeID: artist.ID, ToNodeID: soloAlbum.ID},
			{ID: relID(2), FromNodeID: artist.ID, ToNodeID: collab.ID},
			{ID: relID(3), FromNodeID: other.ID, ToNodeID: collab.ID},
		}
		for _, e := range edges {
			e.Type = v1.RelationSameFranchise
			e.Confidence = 1
			e.Origin = v1.OriginProviderSupplied
			e.CreatedAt = now
			if _, err := d.Relations.Create(c, e); err != nil {
				t.Fatalf("Create edge: %v", err)
			}
		}
		credited, err := d.Relations.ListTo(c, collab.ID, "")
		if err != nil {
			t.Fatalf("ListTo: %v", err)
		}
		if len(credited) != 2 {
			t.Fatalf("the collaboration has %d credited artists, want 2", len(credited))
		}
	})

	// A collected edition is its own Work, related to what it collects by
	// collection_member — the same mechanism as any other collection. A
	// Collection is not a second concept: it is a Node with media_type
	// collection and no items of its own.
	t.Run("a collected edition is its own work joined by collection_member", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		omnibus := newWork(nodeID(1), v1.MediaComicSeries, "Saga: Compendium One", now)
		if _, err := d.Nodes.Create(c, omnibus); err != nil {
			t.Fatalf("Create omnibus: %v", err)
		}
		// The omnibus has its own item structure — it is a real book, not a
		// view over the issues.
		omnibusItem := newItem(nodeID(2), omnibus.ID, omnibus.ID, v1.MediaComicSeries, v1.ItemIssue, "Compendium One", 1, now)
		if _, err := d.Nodes.Create(c, omnibusItem); err != nil {
			t.Fatalf("Create omnibus item: %v", err)
		}

		run := newWork(nodeID(3), v1.MediaComicSeries, "Saga", now)
		if _, err := d.Nodes.Create(c, run); err != nil {
			t.Fatalf("Create run: %v", err)
		}
		for i := range 3 {
			issue := newItem(nodeID(10+i), run.ID, run.ID, v1.MediaComicSeries, v1.ItemIssue,
				fmt.Sprintf("Saga #%d", i+1), float64(i+1), now)
			if _, err := d.Nodes.Create(c, issue); err != nil {
				t.Fatalf("Create issue: %v", err)
			}
		}

		if _, err := d.Relations.Create(c, v1.Relation{
			ID: relID(1), FromNodeID: omnibus.ID, ToNodeID: run.ID,
			Type: v1.RelationCollectionMember, Confidence: 1,
			Origin: v1.OriginUserConfirmed, CreatedAt: now,
		}); err != nil {
			t.Fatalf("Create collection edge: %v", err)
		}

		// Two independent trees, not one absorbed into the other.
		omnibusTree, err := d.Nodes.ListByWork(c, omnibus.ID)
		if err != nil {
			t.Fatalf("ListByWork(omnibus): %v", err)
		}
		runTree, err := d.Nodes.ListByWork(c, run.ID)
		if err != nil {
			t.Fatalf("ListByWork(run): %v", err)
		}
		if len(omnibusTree) != 2 {
			t.Fatalf("omnibus tree = %v, want its own work and item", titles(omnibusTree))
		}
		if len(runTree) != 4 {
			t.Fatalf("run tree = %v, want the work and three issues", titles(runTree))
		}
		// And they are joined only by the edge.
		edges, err := d.Relations.ListFrom(c, omnibus.ID, v1.RelationCollectionMember)
		if err != nil {
			t.Fatalf("ListFrom: %v", err)
		}
		if len(edges) != 1 || edges[0].ToNodeID != run.ID {
			t.Fatalf("collection edges = %+v", edges)
		}
	})

	// An anime and its source manga are two Works joined by adaptation. They
	// have different part structures and frequently diverge in canon, so
	// forcing one tree would corrupt both.
	t.Run("an anime and its source manga stay two works", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		anime := newWork(nodeID(1), v1.MediaAnimeSeries, "Fullmetal Alchemist: Brotherhood", now)
		manga := newWork(nodeID(2), v1.MediaMangaSeries, "Fullmetal Alchemist", now)
		for _, n := range []v1.Node{anime, manga} {
			if _, err := d.Nodes.Create(c, n); err != nil {
				t.Fatalf("Create %s: %v", n.Title, err)
			}
		}

		// The anime nests seasons over episodes.
		season := newContainer(nodeID(3), anime.ID, anime.ID, v1.MediaAnimeSeries, v1.ContainerSeason, "Season 1", 1, now)
		if _, err := d.Nodes.Create(c, season); err != nil {
			t.Fatalf("Create season: %v", err)
		}
		episode := newItem(nodeID(4), anime.ID, season.ID, v1.MediaAnimeSeries, v1.ItemEpisode, "Fullmetal Alchemist", 1, now)
		if _, err := d.Nodes.Create(c, episode); err != nil {
			t.Fatalf("Create episode: %v", err)
		}

		// The manga nests volumes over chapters — a genuinely different shape,
		// which is the reason the two trees are not merged.
		volume := newContainer(nodeID(5), manga.ID, manga.ID, v1.MediaMangaSeries, v1.ContainerVolume, "Volume 1", 1, now)
		if _, err := d.Nodes.Create(c, volume); err != nil {
			t.Fatalf("Create volume: %v", err)
		}
		chapter := newItem(nodeID(6), manga.ID, volume.ID, v1.MediaMangaSeries, v1.ItemChapter, "The Two Alchemists", 1, now)
		if _, err := d.Nodes.Create(c, chapter); err != nil {
			t.Fatalf("Create chapter: %v", err)
		}

		if _, err := d.Relations.Create(c, v1.Relation{
			ID: relID(1), FromNodeID: anime.ID, ToNodeID: manga.ID,
			Type: v1.RelationAdaptation, Confidence: 1,
			Origin: v1.OriginProviderSupplied, CreatedAt: now,
		}); err != nil {
			t.Fatalf("Create adaptation: %v", err)
		}

		// Both are roots; neither is inside the other.
		animeTree, err := d.Nodes.ListByWork(c, anime.ID)
		if err != nil {
			t.Fatalf("ListByWork(anime): %v", err)
		}
		mangaTree, err := d.Nodes.ListByWork(c, manga.ID)
		if err != nil {
			t.Fatalf("ListByWork(manga): %v", err)
		}
		if len(animeTree) != 3 || len(mangaTree) != 3 {
			t.Fatalf("trees = %d anime, %d manga; each should be self-contained", len(animeTree), len(mangaTree))
		}
		for _, n := range animeTree {
			if n.WorkID != anime.ID {
				t.Fatalf("node %q leaked out of the anime work", n.Title)
			}
		}
		// The container types differ, which is the divergence being preserved.
		if animeTree[1].ContainerType == mangaTree[1].ContainerType {
			t.Fatal("the two works should not share a container shape")
		}

		adaptations, err := d.Relations.ListTo(c, manga.ID, v1.RelationAdaptation)
		if err != nil {
			t.Fatalf("ListTo: %v", err)
		}
		if len(adaptations) != 1 || adaptations[0].FromNodeID != anime.ID {
			t.Fatalf("adaptation edges = %+v", adaptations)
		}
	})

	// A 24/7 channel generates thousands of ephemeral guide entries a month,
	// and running identity, merge and relation machinery over them is waste
	// rather than correctness. The channel is a Node; the programmes are not,
	// and this slice ships no table for them.
	t.Run("an iptv channel is a node and its programmes are not", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		channel := newWork(nodeID(1), v1.MediaIPTVChannel, "BBC One HD", now)
		if _, err := d.Nodes.Create(c, channel); err != nil {
			t.Fatalf("Create channel: %v", err)
		}
		// The stream itself is the playable item — one item, one Part, and no
		// per-programme structure beneath it.
		stream := newItem(nodeID(2), channel.ID, channel.ID, v1.MediaIPTVChannel, v1.ItemFeature, "Live", 1, now)
		if _, err := d.Nodes.Create(c, stream); err != nil {
			t.Fatalf("Create stream item: %v", err)
		}
		live := newPart(partID(1), stream.ID, v1.PartEdition, "", 1, now)
		live.Location = v1.MediaLocation{
			Scheme: v1.RemoteLocation, Provider: "iptv", Ref: "http://example.invalid/bbc1.m3u8",
		}
		live.Duration = 0 // A live stream has no duration.
		if _, err := d.Parts.Create(c, live); err != nil {
			t.Fatalf("Create stream part: %v", err)
		}

		// The whole channel is two nodes, however many programmes air on it.
		tree, err := d.Nodes.ListByWork(c, channel.ID)
		if err != nil {
			t.Fatalf("ListByWork: %v", err)
		}
		if len(tree) != 2 {
			t.Fatalf("channel tree = %v, want just the channel and its stream", titles(tree))
		}
		if kids, err := d.Nodes.ListChildren(c, stream.ID); err != nil || len(kids) != 0 {
			t.Fatalf("the stream must have no programme children (err %v, got %v)", err, titles(kids))
		}
		// It is playable without any of the identity machinery having run.
		parts, err := d.Parts.ListByNode(c, stream.ID)
		if err != nil {
			t.Fatalf("ListByNode: %v", err)
		}
		if len(parts) != 1 || parts[0].Local() {
			t.Fatalf("channel parts = %+v, want one remote reference", parts)
		}
	})
}

// RunContentAtomicityContract proves the four content stores share the
// transaction they are reached through, and share it with the outbox.
//
// One transaction spans one context's stores plus the shared event outbox
// (ADR 0014), which is the whole reason Tx enumerates stores by name. A
// failure part-way through must leave nothing behind — not the node, not its
// part, and not the event announcing it.
func RunContentAtomicityContract(t *testing.T, newDeps Factory) {
	now := time.Now().UTC().Truncate(time.Millisecond)

	t.Run("a node, its part and its event commit together", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		work := newWork(nodeID(1), v1.MediaMovie, "Arrival", now)
		item := newItem(nodeID(2), work.ID, work.ID, v1.MediaMovie, v1.ItemFeature, "Arrival", 1, now)

		err := d.UnitOfWork.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
			if _, err := tx.Nodes().Create(c, work); err != nil {
				return err
			}
			if _, err := tx.Nodes().Create(c, item); err != nil {
				return err
			}
			if _, err := tx.Parts().Create(c, newPart(partID(1), item.ID, v1.PartEdition, "", 1, now)); err != nil {
				return err
			}
			if _, err := tx.SourceBindings().Create(c, v1.SourceBinding{
				ID: bindID(1), NodeID: work.ID, SourceProvider: "tmdb", SourceRef: "329865",
				MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact,
				Status: v1.BindingConfirmed, CreatedAt: now, UpdatedAt: now,
			}); err != nil {
				return err
			}
			return tx.Outbox().Append(c, domain.OutboxEvent{Event: domain.Event{
				ID: "content-added-1", Type: "content.node.created",
				Payload: []byte(`{"node":"arrival"}`), OccurredAt: now,
			}})
		})
		if err != nil {
			t.Fatalf("WithinTx: %v", err)
		}

		if _, err := d.Nodes.FindByID(c, item.ID); err != nil {
			t.Fatalf("node should have committed: %v", err)
		}
		parts, err := d.Parts.ListByNode(c, item.ID)
		if err != nil || len(parts) != 1 {
			t.Fatalf("part should have committed (err %v, got %d)", err, len(parts))
		}
		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 1 || events[0].Type != "content.node.created" {
			t.Fatalf("outbox = %+v, want the content event", events)
		}
	})

	t.Run("a failure part-way through leaves nothing behind", func(t *testing.T) {
		d := newDeps(t)
		c := ctx(t)

		work := newWork(nodeID(1), v1.MediaMovie, "Arrival", now)
		item := newItem(nodeID(2), work.ID, work.ID, v1.MediaMovie, v1.ItemFeature, "Arrival", 1, now)
		boom := errors.New("capability failed after writing")

		err := d.UnitOfWork.WithinTx(c, func(c context.Context, tx contracts.Tx) error {
			if _, err := tx.Nodes().Create(c, work); err != nil {
				return err
			}
			if _, err := tx.Nodes().Create(c, item); err != nil {
				return err
			}
			if _, err := tx.Parts().Create(c, newPart(partID(1), item.ID, v1.PartEdition, "", 1, now)); err != nil {
				return err
			}
			if err := tx.Outbox().Append(c, domain.OutboxEvent{Event: domain.Event{
				ID: "content-added-1", Type: "content.node.created",
				Payload: []byte(`{}`), OccurredAt: now,
			}}); err != nil {
				return err
			}
			return boom
		})
		if !errors.Is(err, boom) {
			t.Fatalf("WithinTx error = %v, want the caller's error", err)
		}

		requireCategory(t, mustErr(d.Nodes.FindByID(c, work.ID)), contracts.NotFound)
		requireCategory(t, mustErr(d.Nodes.FindByID(c, item.ID)), contracts.NotFound)
		requireCategory(t, mustErr(d.Parts.FindByID(c, partID(1))), contracts.NotFound)

		events, err := d.Outbox.ListUnpublished(c, 10)
		if err != nil {
			t.Fatalf("ListUnpublished: %v", err)
		}
		if len(events) != 0 {
			t.Fatalf("outbox held %d events after a rollback", len(events))
		}
	})
}

// mustErr discards a store method's value so its error can be asserted inline.
func mustErr[T any](_ T, err error) error { return err }
