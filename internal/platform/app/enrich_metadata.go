// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"slices"
	"strconv"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Metadata enrichment and the tree top-up (platform#62).
//
// The third pass beside streams (platform#46) and artwork (sdk#6). Without it a
// library detail opened by node id has nothing to draw with, and a series that
// gains a season never grows: every module dedups before writing and returns
// AlreadyKnown, so a re-import refreshes everything about a title except its
// shape.
//
// Best-effort by construction, like its two neighbours. A provider that is
// unreachable leaves the last stored document in place, and an import that
// produced a tree has succeeded whether or not anything could be said about it.

// maxTopUpEpisodes bounds how many episode nodes one pass may create.
//
// A provider answering with a decade of a daily show would otherwise turn one
// maintenance item into thousands of writes inside a single job attempt — the
// "a rule can produce a large library quickly" surprise platform#60 names, one
// level further down. Hitting the bound is reported rather than silent, and the
// next run continues from where this one stopped because the pass only ever adds
// what is missing.
const maxTopUpEpisodes = 400

// enrichMetadata stores what a metadata provider says about a materialised work
// and adds the parts of its tree that are missing.
//
// It takes the ref because that is what a MetadataProvider is addressed by, and
// the work id because that is what the result is stored against — the two are
// the same title and neither substitutes for the other.
func (s *Service) enrichMetadata(ctx context.Context, caller v1.Caller, ref v1.ContentRef, workID v1.NodeID) {
	// An import's enrichment is best-effort and says so by discarding the error:
	// a title that materialised has succeeded whether or not anything could be
	// said about it.
	_ = s.enrichMetadataErr(ctx, caller, ref, workID)
}

// enrichMetadataErr is enrichMetadata with the failure reported.
//
// The refresh needs the distinction its caller above throws away: a pass that
// counted an unreachable provider as a successful check would move the row to
// the back of the queue with a fresh timestamp and a stale answer.
func (s *Service) enrichMetadataErr(ctx context.Context, caller v1.Caller, ref v1.ContentRef, workID v1.NodeID) error {
	if s.nodeMetadata == nil || workID == "" {
		// No store keeps nothing, and a detail renders from the node alone. A
		// Service built without one is a test service and must not silently
		// start writing.
		return nil
	}
	provider, ok := s.capabilityMetadataProvider(ref.Provider)
	if !ok {
		return contracts.NewError(contracts.NotFound, "no metadata provider registered under id "+ref.Provider)
	}
	settings, err := s.readModuleSettings(ctx, ref.Provider)
	if err != nil {
		telemetry.From(ctx).Warn("metadata enrichment could not read module settings",
			telemetry.String("module", ref.Provider), telemetry.Err(err))
		return err
	}

	mctx, span := moduleSpan(ctx, ref.Provider, "metadata")
	meta, err := provider.Metadata(mctx, v1.MetadataRequest{Caller: caller, Settings: settings, Ref: ref})
	failSpan(span, err)
	span.End()
	if err != nil {
		telemetry.From(ctx).Warn("metadata provider failed during enrichment",
			telemetry.String("module", ref.Provider), telemetry.Err(err))
		return err
	}

	s.storeMetadata(ctx, ref.Provider, workID, meta)
	s.storeAvailability(ctx, workID, meta.Watch)
	s.storeGenres(ctx, workID, meta.Genres)
	s.topUpTree(ctx, caller, workID, meta.Episodes)
	return nil
}

// storeGenres keeps the work's stored genres in step with what its source now
// says (roadmap M2.4).
//
// Without this the facet is empty, silently, on every library that predates it:
// a module writes genres on AddContentWork and every module dedups before
// writing, so a title already in the library is never re-added and never gains
// them. Compare the stale-artwork follow-up platform#45 records as owed; a
// missing genre is worse, because a chip nobody is offered is invisible where an
// unrefreshed poster is not.
//
// It replaces rather than merges: the genres are one source's whole answer about
// one title, and a union would accumulate every name a provider ever used and
// never drop one it retired.
//
// It writes only when the value differs, so an ordinary refresh of an unchanged
// title costs a read and no write.
func (s *Service) storeGenres(ctx context.Context, workID v1.NodeID, genres []string) {
	node, err := s.nodes.FindByID(ctx, workID)
	if err != nil {
		return
	}
	if slices.Equal(node.Genres, genres) {
		return
	}
	node.Genres = genres
	if _, err := s.nodes.Update(ctx, node); err != nil {
		telemetry.From(ctx).Warn("metadata enrichment could not store genres",
			telemetry.String("node_id", string(workID)), telemetry.Err(err))
	}
}

// storeAvailability writes the queryable projection of where a work can be
// watched (roadmap M2.5).
//
// It must be written from ContentMetadata.Watch, the typed value the SDK
// carries, and never from a module's attributes document. That is what keeps the
// facet free of any module's vocabulary: module-tmdb records its own tmdbWatch
// document at import and that stays its business, while any metadata provider
// that fills Watch populates this.
//
// A provider that answered with nothing still writes a row. A title that has
// left every service must stop matching the facet, and the only way it can is
// for the empty answer to overwrite the old one; skipping the write would freeze
// the last positive answer forever.
func (s *Service) storeAvailability(ctx context.Context, workID v1.NodeID, watch *v1.WatchAvailability) {
	if s.watchAvailability == nil {
		return
	}
	record := contracts.WatchAvailability{NodeID: workID, CheckedAt: s.clock.Now()}
	if watch != nil {
		record.Region = watch.Region
		for _, offer := range watch.Offers {
			if offer.Provider != "" && !slices.Contains(record.Providers, offer.Provider) {
				// Flattened and de-duplicated: one service offering a title to
				// subscribe and to buy is one chip, not two.
				record.Providers = append(record.Providers, offer.Provider)
			}
		}
	}
	if _, err := s.watchAvailability.Upsert(ctx, record); err != nil {
		telemetry.From(ctx).Warn("metadata enrichment could not store availability",
			telemetry.String("node_id", string(workID)), telemetry.Err(err))
	}
}

// storeMetadata writes the provider's answer against the work.
//
// Episodes are stripped before storing. They are a projection of the tree, which
// the top-up below makes authoritative; a second copy in the document would give
// a detail screen two answers to "what episodes are there", and they would
// disagree the moment a tree grew and the cache did not.
func (s *Service) storeMetadata(ctx context.Context, moduleID string, workID v1.NodeID, meta v1.ContentMetadata) {
	meta.Episodes = nil
	document, err := json.Marshal(meta)
	if err != nil {
		telemetry.From(ctx).Warn("metadata enrichment could not encode the provider's answer",
			telemetry.String("node_id", string(workID)), telemetry.Err(err))
		return
	}
	// Written outside the caller's transaction and through the direct handle,
	// like the probe recording: an import is several transactions already, and a
	// cache write inside one could roll back the materialisation it describes.
	if _, err := s.nodeMetadata.Upsert(ctx, contracts.NodeMetadata{
		NodeID: workID, Document: document, Source: moduleID, FetchedAt: s.clock.Now(),
	}); err != nil {
		telemetry.From(ctx).Warn("metadata enrichment could not store the provider's answer",
			telemetry.String("node_id", string(workID)), telemetry.Err(err))
	}
}

// topUpTree adds the seasons and episodes a work is missing.
//
// This is the Platform building a tree, which platform#18 gave to the
// materialising capability and platform#62 takes part of back on platform#46's
// ground: season and episode are facts about television the Platform already
// models. Nothing here composes a provider's own addressing — it reads two
// integers the SDK carries neutrally and writes them through the same public
// commands a module would.
//
// It adds and never removes. An episode that has left a source's listing stays;
// do not add a deletion path here.
func (s *Service) topUpTree(ctx context.Context, caller v1.Caller, workID v1.NodeID, episodes []v1.EpisodePreview) {
	if len(episodes) == 0 {
		return
	}
	nodes, err := s.nodes.ListByWork(ctx, workID)
	if err != nil {
		telemetry.From(ctx).Warn("tree top-up could not read the work",
			telemetry.String("work_id", string(workID)), telemetry.Err(err))
		return
	}
	work, _, _ := indexWork(workID, nodes)
	if work.ID == "" {
		return
	}

	// What the tree already holds, by the coordinates the provider speaks in. A
	// season container's NaturalOrder is its season number and an episode item's
	// is its episode number — the same reading coordinatesOf makes for the stream
	// fan-out, so the two passes cannot disagree about which episode is which.
	seasons := map[int]v1.NodeID{}
	have := map[[2]int]v1.Node{}
	byID := make(map[v1.NodeID]v1.Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
	}
	for _, node := range nodes {
		if node.Kind == v1.NodeContainer && node.ContainerType == v1.ContainerSeason {
			seasons[int(node.NaturalOrder)] = node.ID
		}
	}
	for _, node := range nodes {
		if node.Kind != v1.NodeItem {
			continue
		}
		season, episode := coordinatesOf(node, byID)
		have[[2]int{season, episode}] = node
	}

	created := 0
	for _, preview := range episodes {
		if preview.Season <= 0 || preview.Episode <= 0 {
			// A special, or a source that numbers nothing. There is no
			// coordinate to place it at and inventing one would put it in a
			// season it is not in — skipped rather than guessed.
			continue
		}
		if existing, ok := have[[2]int{preview.Season, preview.Episode}]; ok {
			// Already in the tree. Fill in a still it does not have — the same
			// "only what is missing" rule the Parts pass follows. It is also why
			// an episode a module created draws an image at all: a module
			// building a tree carries no per-episode art.
			s.fillEpisodeStill(ctx, caller, existing, preview.Thumbnail)
			continue
		}
		if created >= maxTopUpEpisodes {
			telemetry.From(ctx).Warn("tree top-up stopped on its bound; the next run continues",
				telemetry.String("work_id", string(workID)),
				telemetry.Int("created", created))
			break
		}

		seasonID, ok := seasons[preview.Season]
		if !ok {
			container, err := s.AddContentChild(ctx, v1.AddContentChildCommand{
				// No media type: a child inherits it from its parent
				// (platform#9), so a season cannot declare a different one than
				// its series and this pass cannot invent one.
				Caller: caller, ParentID: workID, Kind: v1.NodeContainer,
				ContainerType: v1.ContainerSeason,
				Title:         seasonTitle(preview.Season),
				NaturalOrder:  float64(preview.Season),
			})
			if err != nil {
				telemetry.From(ctx).Warn("tree top-up could not add a season",
					telemetry.String("work_id", string(workID)),
					telemetry.Int("season", preview.Season), telemetry.Err(err))
				// The whole season's episodes have nowhere to hang, so stop
				// rather than attaching them to the work and inventing a shape
				// no source described.
				break
			}
			seasonID = container.Node.ID
			seasons[preview.Season] = seasonID
		}

		_, err := s.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: caller, ParentID: seasonID, Kind: v1.NodeItem,
			ItemType:     v1.ItemEpisode,
			Title:        episodeTitle(preview),
			NaturalOrder: float64(preview.Episode),
			// The still, stored on the node like every other image (platform#45)
			// so an episode row draws without a provider round trip.
			Artwork: v1.Artwork{Landscape: preview.Thumbnail},
		})
		if err != nil {
			telemetry.From(ctx).Warn("tree top-up could not add an episode",
				telemetry.String("work_id", string(workID)),
				telemetry.Int("season", preview.Season),
				telemetry.Int("episode", preview.Episode), telemetry.Err(err))
			continue
		}
		have[[2]int{preview.Season, preview.Episode}] = v1.Node{}
		created++
	}

	if created > 0 {
		telemetry.From(ctx).Info("tree topped up",
			telemetry.String("work_id", string(workID)),
			telemetry.Int("episodes_added", created))
	}
}

// fillEpisodeStill gives an existing episode the still it is missing.
//
// Only when it has none. An episode whose art was set by its module, or chosen
// by the artwork pass (platform#47), is left exactly as it is: this fills a gap
// and must never overrule a choice, which is what keeps the pass safe to run on
// a schedule.
func (s *Service) fillEpisodeStill(ctx context.Context, caller v1.Caller, node v1.Node, thumbnail string) {
	if thumbnail == "" || node.ID == "" {
		return
	}
	if node.Artwork.Landscape != "" || node.Artwork.Poster != "" {
		return
	}
	art := node.Artwork
	art.Landscape = thumbnail
	if _, err := s.SetContentArtwork(ctx, v1.SetContentArtworkCommand{
		Caller: caller, NodeID: node.ID, Artwork: art,
	}); err != nil {
		telemetry.From(ctx).Warn("tree top-up could not fill an episode still",
			telemetry.String("node_id", string(node.ID)), telemetry.Err(err))
	}
}

// seasonTitle names a season container. The Platform's own words, because the
// container is the Platform's own node — a provider names episodes and does not
// generally name seasons, and a container called "" is a row nothing can render.
func seasonTitle(season int) string {
	return "Season " + strconv.Itoa(season)
}

// episodeTitle is what a source called the episode, or its number when it said
// nothing. An untitled row is unclickable in a list of untitled rows.
func episodeTitle(preview v1.EpisodePreview) string {
	if preview.Title != "" {
		return preview.Title
	}
	return "Episode " + strconv.Itoa(preview.Episode)
}

// StoredMetadata reads back what a provider said about a node, and whether
// anything was ever stored.
//
// The bool rather than an error, because "never enriched" is an ordinary state a
// caller renders around: a node whose provider has never been reachable still
// has a title and artwork of its own to draw.
func (s *Service) StoredMetadata(ctx context.Context, nodeID v1.NodeID) (v1.ContentMetadata, bool) {
	if s.nodeMetadata == nil {
		return v1.ContentMetadata{}, false
	}
	stored, err := s.nodeMetadata.Get(ctx, nodeID)
	if err != nil {
		return v1.ContentMetadata{}, false
	}
	var meta v1.ContentMetadata
	if err := json.Unmarshal(stored.Document, &meta); err != nil {
		telemetry.From(ctx).Warn("stored metadata could not be decoded",
			telemetry.String("node_id", string(nodeID)), telemetry.Err(err))
		return v1.ContentMetadata{}, false
	}
	return meta, true
}
