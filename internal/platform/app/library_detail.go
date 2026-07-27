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

// Reading a library item's detail out of the object graph (ADR 0107).
//
// It is the read the Library screen needed and did not have. A card there opens
// its node by **id** — the whole point of a screen over the graph is that it
// still opens when the source is down — and ADR 0034's detail is keyed by a
// **ref** and re-derived from the provider on every render, so the two never
// met and the node-id path fell back to a title and a list of children.
//
// One query rather than three calls from the emit-side, because it is one
// question: what does the install know about this title? The alternative was a
// screen making a node read, a tree read and a document read, each through its
// own boundary, and getting the ordering between them right.

// GetLibraryDetailQuery reads everything the graph holds about one work.
type GetLibraryDetailQuery struct {
	Caller v1.Caller
	NodeID v1.NodeID
}

// GetLibraryDetailResult is the node, its whole tree, and what a provider last
// said about it.
type GetLibraryDetailResult struct {
	// Node is the work itself.
	Node v1.Node
	// Tree is every node beneath it — seasons and episodes — ordered as the
	// graph orders them. It is the *authority* on what episodes exist, which is
	// why the stored document deliberately carries none (ADR 0107).
	Tree []v1.Node
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
// (ADR 0103).
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

	// The whole tree in one read rather than a walk per season. ListByWork reads
	// the denormalised work id, so a series with ten seasons costs the same one
	// query as a film — which is what lets this screen show every season's
	// episodes without a read per season.
	tree, err := s.nodes.ListByWork(ctx, node.WorkID)
	if err != nil {
		return GetLibraryDetailResult{}, err
	}
	out := GetLibraryDetailResult{Node: node}
	for _, n := range tree {
		if n.ID != node.ID {
			out.Tree = append(out.Tree, n)
		}
	}

	if s.nodeMetadata != nil {
		// Stored against the work, not against whichever node was opened: a
		// season has no description of its own and its series' is the right
		// answer for it.
		if stored, err := s.nodeMetadata.Get(ctx, node.WorkID); err == nil {
			var meta v1.ContentMetadata
			if json.Unmarshal(stored.Document, &meta) == nil {
				out.Metadata = meta
				out.HasMetadata = true
			}
		}
	}
	return out, nil
}

// EpisodesFromTree projects a work's materialised tree back into the episode
// preview shape the detail screen renders.
//
// **The tree is the authority and the document carries no episodes** (ADR 0107),
// so this is the one direction the projection runs for a library item. It is
// exported because the emit-side is what needs it and this is a pure function of
// the tree — putting it on the query result would make it look like something
// the store returned.
//
// A season container's NaturalOrder is its season number and an episode item's
// is its episode number, which is the same reading the stream fan-out and the
// tree top-up both make, so the three cannot disagree about which episode is
// which.
func EpisodesFromTree(tree []v1.Node) []v1.EpisodePreview {
	// (see episodeStill for why an episode's image is looked for in two slots)
	seasonOf := map[v1.NodeID]int{}
	for _, n := range tree {
		if n.Kind == v1.NodeContainer && n.ContainerType == v1.ContainerSeason {
			seasonOf[n.ID] = int(n.NaturalOrder)
		}
	}

	var out []v1.EpisodePreview
	for _, n := range tree {
		if n.Kind != v1.NodeItem || n.ItemType != v1.ItemEpisode || n.ParentID == nil {
			continue
		}
		season, ok := seasonOf[*n.ParentID]
		if !ok {
			// An episode hanging off something that is not a season. It exists
			// and it is not placeable in a season list, so it is left out of
			// this projection rather than filed under season zero.
			continue
		}
		out = append(out, v1.EpisodePreview{
			Season:    season,
			Episode:   int(n.NaturalOrder),
			Title:     n.Title,
			Thumbnail: episodeStill(n.Artwork),
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Season != out[j].Season {
			return out[i].Season < out[j].Season
		}
		return out[i].Episode < out[j].Episode
	})
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
