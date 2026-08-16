// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"sort"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Stream enrichment (platform#46).
//
// Import is two steps: the capability the ref names builds the tree, and then
// this asks every registered stream provider to fill in what plays. A metadata
// module fills no stream role, so a title materialised from one is a Work and a
// season/episode tree with nothing playable in it while a stream source sits
// registered alongside, able to resolve that exact title.

// enrichStreams resolves playable locations for the items of a materialised
// work and attaches them as Parts.
//
// Best-effort by construction: every failure path here logs and continues. A
// stream source that is down, unconfigured, or that does not know the title must
// not lose a user the work they just added — an item with no Parts is a valid
// outcome and is what a metadata-only deployment produces.
func (s *Service) enrichStreams(ctx context.Context, caller v1.Caller, workID v1.NodeID, result *v1.ImportResult) {
	providers := s.capabilities.StreamProviders()
	if len(providers) == 0 {
		return
	}
	// The parts read handle is what makes this idempotent: without it there is no
	// way to tell an item that needs filling from one already filled, and a
	// re-import would attach a second copy of every release. A Service composed
	// without it does not enrich rather than enriching unsafely; the serving
	// composition always sets it.
	if s.parts == nil {
		return
	}

	nodes, err := s.nodes.ListByWork(ctx, workID)
	if err != nil {
		telemetry.From(ctx).Warn("stream enrichment could not read the work",
			telemetry.String("work_id", string(workID)), telemetry.Err(err))
		return
	}

	work, byID, items := indexWork(workID, nodes)
	if len(items) == 0 {
		return
	}

	// The identities a stream provider might recognise. A provider is handed the
	// work's shared external ids rather than a native one, because it did not
	// source this content and holds no id of its own for it. Sorted so a run is
	// reproducible rather than depending on map order.
	identities := sharedIdentitiesOf(work)
	if len(identities) == 0 {
		// Nothing neutral to offer. A source keyed only to itself is unenrichable
		// by design rather than by oversight (platform#46).
		return
	}

	for _, item := range items {
		// Only items with nothing playable, which is what makes the pass
		// idempotent: re-importing a title that already has releases does not
		// attach a second copy, while an item that got none the first time is
		// filled in on the next import. It also leaves a module that attached
		// its own Parts alone.
		existing, err := s.parts.ListByNode(ctx, item.ID)
		if err != nil {
			telemetry.From(ctx).Warn("stream enrichment could not read existing parts",
				telemetry.String("node_id", string(item.ID)), telemetry.Err(err))
			continue
		}
		if len(existing) > 0 {
			continue
		}

		season, episode := coordinatesOf(item, byID)

		for _, provider := range providers {
			settings, err := s.readModuleSettings(ctx, provider.ModuleID)
			if err != nil {
				telemetry.From(ctx).Warn("stream enrichment could not read module settings",
					telemetry.String("module", provider.ModuleID), telemetry.Err(err))
				continue
			}

			attached := false
			for _, identity := range identities {
				ref := v1.ContentRef{
					Provider:       provider.ModuleID,
					MediaType:      work.MediaType,
					ExternalScheme: identity.Scheme,
					ExternalID:     identity.ID,
				}

				mctx, span := moduleSpan(ctx, provider.ModuleID, "streams")
				resp, err := provider.Provider.Streams(mctx, v1.StreamRequest{
					Caller: caller, Settings: settings, Ref: ref,
					Season: season, Episode: episode,
				})
				failSpan(span, err)
				span.End()
				if err != nil {
					telemetry.From(ctx).Warn("stream provider failed during enrichment",
						telemetry.String("module", provider.ModuleID),
						telemetry.String("scheme", identity.Scheme),
						telemetry.Err(err))
					continue
				}
				if len(resp.Streams) == 0 {
					// Declining is the normal answer for an identity a provider
					// does not speak, so it is not worth a record.
					continue
				}

				s.attachResolvedStreams(ctx, caller, item.ID, provider.ModuleID, resp.Streams, result)
				attached = true
				break
			}
			if attached {
				// One provider's set is enough for this item. Merging several
				// providers' releases needs cross-provider dedup, which does not
				// exist — platform#46 leaves it open rather than guessing.
				break
			}
		}
	}
}

// attachResolvedStreams writes one provider's stream locations onto an item.
//
// It goes through the public command exactly as a module would, deliberately:
// this is a module's work being done on a module's behalf, so it pays the same
// authorisation and takes the same path a module's own import takes for every
// Part it attaches.
func (s *Service) attachResolvedStreams(ctx context.Context, caller v1.Caller, nodeID v1.NodeID, moduleID string, streams []v1.StreamLink, result *v1.ImportResult) {
	for i, stream := range streams {
		cmd := partCommandFor(nodeID, i, stream)
		cmd.Caller = caller
		if _, err := s.AttachContentPart(ctx, cmd); err != nil {
			telemetry.From(ctx).Warn("stream enrichment could not attach a part",
				telemetry.String("node_id", string(nodeID)),
				telemetry.String("module", moduleID), telemetry.Err(err))
			continue
		}
		result.Parts++
	}
}

// partCommandFor translates one resolved candidate into the command that
// attaches it.
//
// Every descriptive field on StreamLink must be carried across. The fields exist
// on StreamLink and on AttachContentPartCommand under the same names and types
// deliberately, so moving a candidate onto a Part is a copy rather than a
// translation; a field dropped here silently discards what a module parsed at
// its own boundary (module-stremio-addons#2) and leaves the probe as the only
// thing that ever knows what a release is.
//
// It is a separate function so a test can reach the mapping without standing up
// a Service and a boundary.
func partCommandFor(nodeID v1.NodeID, order int, stream v1.StreamLink) v1.AttachContentPartCommand {
	return v1.AttachContentPartCommand{
		NodeID: nodeID, Role: v1.PartEdition,
		EditionLabel: editionLabelOf(stream),
		// Preserves the provider's own ranking, so a consumer that expresses
		// no preference still gets the order the source intended.
		NaturalOrder: float64(order),
		Location:     stream.Location,
		SizeBytes:    stream.SizeBytes,
		// A parse of release text rather than a measurement — the probe still
		// settles what a release actually is (platform#29) — but it is what makes
		// a candidate list rankable before anything has been fetched, which is
		// what selection reads.
		Container:  stream.Container,
		VideoCodec: stream.VideoCodec,
		AudioCodec: stream.AudioCodec,
		// The inputs to the two most expensive outcomes selection can choose:
		// sending a client more pixels than it can display, and tone-mapping a
		// stream it cannot render. Quality is a display label rather than a
		// number, so without these a source offering an HDR and an SDR cut of one
		// title cannot be told to prefer the one that plays untouched.
		Width:     stream.Width,
		Height:    stream.Height,
		HDRFormat: stream.HDRFormat,
	}
}

// editionLabelOf is the human name for a candidate — the source's full release
// title where it has one, otherwise its short label.
func editionLabelOf(stream v1.StreamLink) string {
	if stream.Title != "" {
		return stream.Title
	}
	return stream.Label
}

// sharedIdentitiesOf reads the work's external ids into a stable, ordered list.
//
// Every scheme is offered rather than the Platform choosing between them: which
// identities a source speaks is the source's business. Declining an unrecognised
// scheme is cheap on the module side — a comparison, not a request.
//
// The value is the SDK's own v1.ExternalIdentity rather than a private struct,
// because sdk#6's artwork request carries the whole set across the module
// boundary and the Platform should not hold a second shape for the same pair.
func sharedIdentitiesOf(work v1.Node) []v1.ExternalIdentity {
	if len(work.ExternalIDs) == 0 {
		return nil
	}
	var document map[string]string
	if err := json.Unmarshal(work.ExternalIDs, &document); err != nil {
		return nil
	}
	out := make([]v1.ExternalIdentity, 0, len(document))
	for scheme, value := range document {
		if scheme != "" && value != "" {
			out = append(out, v1.ExternalIdentity{Scheme: scheme, ID: value})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scheme < out[j].Scheme })
	return out
}

// indexWork splits a work's nodes into the root, a lookup by id, and its items.
func indexWork(workID v1.NodeID, nodes []v1.Node) (work v1.Node, byID map[v1.NodeID]v1.Node, items []v1.Node) {
	byID = make(map[v1.NodeID]v1.Node, len(nodes))
	for _, node := range nodes {
		byID[node.ID] = node
		switch {
		case node.ID == workID:
			work = node
		case node.Kind == v1.NodeItem:
			items = append(items, node)
		}
	}
	return work, byID, items
}

// coordinatesOf locates an item within its series, as the season and episode
// numbers a stream provider composes its own addressing from.
//
// They are read from the tree's own shape rather than stored separately: a
// season container's NaturalOrder is its season number and an episode item's is
// its episode number, which is what every module that builds a tree already
// writes. A film's feature item hangs directly off the work and has neither, so
// it reports zeroes, which a provider reads as "not an episode".
func coordinatesOf(item v1.Node, byID map[v1.NodeID]v1.Node) (season, episode int) {
	if item.ParentID == nil {
		return 0, 0
	}
	parent, ok := byID[*item.ParentID]
	if !ok || parent.Kind != v1.NodeContainer || parent.ContainerType != v1.ContainerSeason {
		return 0, 0
	}
	return int(parent.NaturalOrder), int(item.NaturalOrder)
}
