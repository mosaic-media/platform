// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Reading a library item's detail out of the object graph (platform#62).
//
// It is the read the Library screen needed and did not have. A card there opens
// its node by **id** — the whole point of a screen over the graph is that it
// still opens when the source is down — and sdk#3's detail is keyed by a
// **ref** and re-derived from the provider on every render, so the two never
// met and the node-id path fell back to a title and a list of children.
//
// One query rather than three calls from the emit-side, because it is one
// question: what does the install know about this title? The alternative was a
// screen making a node read, a tree read and a document read, each through its
// own boundary, and getting the ordering between them right.

// GetLibraryDetailQuery reads what the graph holds about one work, for one
// season of it.
type GetLibraryDetailQuery struct {
	Caller v1.Caller
	NodeID v1.NodeID
	// Season narrows the episode read to one season. Zero takes the first
	// numbered one, which is what a screen opened with no season param wants.
	//
	// It is on the *query* rather than applied by the caller afterwards, and
	// that is the whole point of this shape: a work is read a season at a time
	// because some works are enormous. A daily news programme in this library
	// has 75 seasons and 21,428 episodes, and reading the tree whole to draw
	// seven rows took **a second** — not because PostgreSQL minds 21,000 rows,
	// which it does not, but because sorting and marshalling them per render is
	// a cost paid for nothing.
	Season int
}

// GetLibraryDetailResult is the node, the shape of its tree, one season of it,
// and what a provider last said about it.
type GetLibraryDetailResult struct {
	// Node is the work itself.
	Node v1.Node
	// Children are the work's direct children: a series' season containers, or
	// a film's feature item. They are what the season selector is drawn from and
	// what a work with no stored description shows as its contents.
	Children []v1.Node
	// Episodes are the selected season's items, in order. Together with Children
	// they are the *authority* on what exists, which is why the stored document
	// deliberately carries no episodes (platform#62).
	Episodes []v1.Node
	// Season is which season Episodes belongs to, resolved from the query — so a
	// caller rendering a selector knows which one is lit without repeating the
	// defaulting rule.
	Season int
	// Metadata is the stored provider answer, and HasMetadata says whether one
	// was ever stored.
	//
	// The flag rather than an empty struct, because "never enriched" and
	// "enriched and the source said little" are different facts: the first is a
	// node materialised before this existed or whose provider has never been
	// reachable, and a caller renders a plainer screen for it rather than an
	// apologetic one.
	Metadata    v1.ContentMetadata
	HasMetadata bool
}

// GetLibraryDetail reads one work, its tree and its stored description.
//
// It authorises `content.read`, the same action every other library read takes:
// there is one shared library and everybody who may see it may see all of it
// (platform#59).
func (s *Service) GetLibraryDetail(ctx context.Context, q GetLibraryDetailQuery) (GetLibraryDetailResult, error) {
	// 1. validate query shape.
	if q.Caller.Session == "" {
		return GetLibraryDetailResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.NodeID == "" {
		return GetLibraryDetailResult{}, contracts.NewError(contracts.InvalidArgument, "a node id is required")
	}

	// 2-3. authenticate the caller and authorize the action.
	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{
		Type: "content", ID: string(q.NodeID),
	}); err != nil {
		return GetLibraryDetailResult{}, err
	}

	// 4. load state through read contracts.
	node, err := s.nodes.FindByID(ctx, q.NodeID)
	if err != nil {
		return GetLibraryDetailResult{}, err
	}
	// A season opened directly is rendered as its series, because a season has
	// no description of its own and nobody navigating to one wants a screen that
	// says less than the one they came from.
	work := node
	if node.WorkID != node.ID {
		work, err = s.nodes.FindByID(ctx, node.WorkID)
		if err != nil {
			return GetLibraryDetailResult{}, err
		}
	}

	children, err := s.nodes.ListChildren(ctx, work.ID)
	if err != nil {
		return GetLibraryDetailResult{}, err
	}
	out := GetLibraryDetailResult{Node: work, Children: children}

	// Which season to read, and then only that season. Two indexed reads over
	// the seasons and one season's episodes, rather than one read of the whole
	// work — which is the difference between a screen that opens instantly for a
	// long-running series and one that does not.
	out.Season = pickSeason(children, q.Season)
	for _, child := range children {
		if child.Kind != v1.NodeContainer || int(child.NaturalOrder) != out.Season {
			continue
		}
		episodes, err := s.nodes.ListChildren(ctx, child.ID)
		if err != nil {
			return GetLibraryDetailResult{}, err
		}
		out.Episodes = episodes
		break
	}

	if s.nodeMetadata != nil {
		// Stored against the work, which is why a season opened directly
		// resolved to its series above.
		if stored, err := s.nodeMetadata.Get(ctx, work.ID); err == nil {
			var meta v1.ContentMetadata
			if json.Unmarshal(stored.Document, &meta) == nil {
				out.Metadata = meta
				out.HasMetadata = true
			}
		}
	}
	return out, nil
}

// pickSeason resolves which season to read: the one asked for when the work has
// it, otherwise the first numbered one.
//
// Season zero is skipped when a numbered season exists, because a series' specials
// are not what somebody opening it came for — the same default the season
// selector has always applied, moved here so the read and the selector cannot
// disagree about which season is lit.
func pickSeason(children []v1.Node, want int) int {
	first, found := 0, false
	for _, child := range children {
		if child.Kind != v1.NodeContainer {
			continue
		}
		n := int(child.NaturalOrder)
		if n == want && want != 0 {
			return want
		}
		if !found || (first < 1 && n >= 1) {
			first, found = n, true
		}
	}
	return first
}

// SeasonNumbers is the season a work has, in order — what a season selector is
// drawn from.
//
// It comes from the *containers* rather than from the episodes, which is what
// lets a detail screen list seventy-five seasons while having read only one of
// them. Deriving it from episodes was what forced the whole tree to be read.
func SeasonNumbers(children []v1.Node) []int {
	var out []int
	for _, n := range children {
		if n.Kind == v1.NodeContainer && n.ContainerType == v1.ContainerSeason {
			out = append(out, int(n.NaturalOrder))
		}
	}
	sort.Ints(out)
	return out
}

// EpisodesFromTree projects one season's materialised items into the episode
// preview shape the detail screen renders.
//
// **The tree is the authority and the document carries no episodes** (platform#62),
// so this is the one direction the projection runs for a library item. It is
// exported because the emit-side is what needs it and this is a pure function of
// its arguments — putting it on the query result would make it look like
// something the store returned.
//
// A season container's NaturalOrder is its season number and an episode item's
// is its episode number, which is the same reading the stream fan-out and the
// tree top-up both make, so the three cannot disagree about which episode is
// which.
func EpisodesFromTree(season int, episodes []v1.Node) []v1.EpisodePreview {
	out := make([]v1.EpisodePreview, 0, len(episodes))
	for _, n := range episodes {
		if n.Kind != v1.NodeItem {
			continue
		}
		out = append(out, v1.EpisodePreview{
			Season:  season,
			Episode: int(n.NaturalOrder),
			Title:   n.Title,
			// See episodeStill: an episode's image is looked for in two slots
			// because the fleet disagrees about which one it belongs in.
			Thumbnail: episodeStill(n.Artwork),
		})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Episode < out[j].Episode })
	return out
}

// episodeStill is an episode's image, wherever it was filed.
//
// **Two slots, because the fleet disagrees.** An episode still is
// landscape-shaped and `ArtworkLandscape` is where this Platform writes one, but
// `module-tmdb` has always written it to `Poster` — it is the image the episode
// *has*, and a module choosing the wrong slot for it is not something a read
// should punish a viewer for. Reading both is what turned every episode row in
// the product from a grey rectangle into a still; preferring landscape is what
// keeps a deliberate choice winning over a historical one.
//
// The honest fix is for the module to file it as a landscape, which is a change
// in that repository and a release of it. This is the read-side accommodation
// until then, and it is written down rather than left as a mysterious fallback.
func episodeStill(art v1.Artwork) string {
	if art.Landscape != "" {
		return art.Landscape
	}
	return art.Poster
}
