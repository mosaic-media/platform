// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package reference

import (
	"context"
	"fmt"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Capability sources content from a provider and adds it to the Platform. It
// owns no schema and holds only its provider port and the published service;
// everything it does to the graph goes through ContentService (ADR 0012).
type Capability struct {
	source MetadataSource
}

// New builds the capability over a metadata source.
func New(source MetadataSource) *Capability {
	return &Capability{source: source}
}

// ImportResult reports what an import did, so a caller (or a test) can see
// the shape without re-reading the graph.
type ImportResult struct {
	WorkID       v1.NodeID
	AlreadyKnown bool
	Containers   int
	Items        int
	Parts        int
	// Adaptation is the id of the source work this one adapts, if any.
	Adaptation v1.NodeID
}

// Import sources a work by query and reflects it into the Platform's generic
// model, acting throughout as the caller it is handed (ADR 0017). It is the
// whole thesis in one method: source, search to avoid duplicating, create
// nodes and relations, and — through the commands it issues — cause events.
func (c *Capability) Import(ctx context.Context, svc v1.ContentService, caller v1.Caller, query string) (ImportResult, error) {
	// Source external metadata.
	meta, err := c.source.Fetch(ctx, query)
	if err != nil {
		return ImportResult{}, fmt.Errorf("source metadata: %w", err)
	}
	if meta.Provider == "" || meta.SourceID == "" {
		return ImportResult{}, fmt.Errorf("provider metadata is missing an identity")
	}

	// Search existing content: if this exact source is already resolved,
	// return it rather than creating a second copy.
	if existing, ok, err := c.find(ctx, svc, caller, meta.Provider, meta.SourceID); err != nil {
		return ImportResult{}, err
	} else if ok {
		return ImportResult{WorkID: existing, AlreadyKnown: true}, nil
	}

	// Create the work, and bind the source that identifies it.
	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: caller, MediaType: meta.MediaType, Title: meta.Title,
		ExternalIDs: encodeExternalIDs(meta.ExternalIDs),
	})
	if err != nil {
		return ImportResult{}, fmt.Errorf("create work: %w", err)
	}
	result := ImportResult{WorkID: work.Work.ID}

	if _, err := svc.BindContentSource(ctx, v1.BindContentSourceCommand{
		Caller: caller, NodeID: work.Work.ID,
		SourceProvider: meta.Provider, SourceRef: meta.SourceID,
		MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact, Status: v1.BindingConfirmed,
	}); err != nil {
		return ImportResult{}, fmt.Errorf("bind source: %w", err)
	}

	// Create the containment tree the metadata describes.
	for i, season := range meta.Seasons {
		container, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: caller, ParentID: work.Work.ID,
			Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason,
			Title: season.Title, NaturalOrder: float64(i + 1),
		})
		if err != nil {
			return ImportResult{}, fmt.Errorf("create season %q: %w", season.Title, err)
		}
		result.Containers++

		for j, episode := range season.Episodes {
			item, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
				Caller: caller, ParentID: container.Node.ID,
				Kind: v1.NodeItem, ItemType: v1.ItemEpisode,
				Title: episode.Title, NaturalOrder: float64(j + 1),
			})
			if err != nil {
				return ImportResult{}, fmt.Errorf("create episode %q: %w", episode.Title, err)
			}
			result.Items++

			if episode.FilePath != "" {
				if _, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
					Caller: caller, NodeID: item.Node.ID, Role: v1.PartEdition,
					Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: episode.FilePath},
					Duration: episode.Duration,
				}); err != nil {
					return ImportResult{}, fmt.Errorf("attach part for %q: %w", episode.Title, err)
				}
				result.Parts++
			}
		}
	}

	// An anime and its source manga are two Works joined by an edge, not one
	// tree (ADR 0013). Find or create the source work, then relate to it.
	if meta.Adaptation != nil {
		sourceID, err := c.findOrCreateAdaptation(ctx, svc, caller, *meta.Adaptation)
		if err != nil {
			return ImportResult{}, err
		}
		if _, err := svc.RelateContent(ctx, v1.RelateContentCommand{
			Caller: caller, FromNodeID: work.Work.ID, ToNodeID: sourceID,
			Type: v1.RelationAdaptation, Confidence: 1, Origin: v1.OriginProviderSupplied,
		}); err != nil {
			return ImportResult{}, fmt.Errorf("relate adaptation: %w", err)
		}
		result.Adaptation = sourceID
	}

	return result, nil
}

// find looks for an existing work bound to a provider id.
func (c *Capability) find(ctx context.Context, svc v1.ContentService, caller v1.Caller, provider, sourceID string) (v1.NodeID, bool, error) {
	found, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: caller, Scheme: provider, Value: sourceID,
	})
	if err != nil {
		return "", false, fmt.Errorf("search existing content: %w", err)
	}
	for _, node := range found.Nodes {
		if node.IsRoot() {
			return node.ID, true, nil
		}
	}
	return "", false, nil
}

// findOrCreateAdaptation resolves the source work an anime adapts, creating a
// bare work for it if the library does not have it yet.
func (c *Capability) findOrCreateAdaptation(ctx context.Context, svc v1.ContentService, caller v1.Caller, a AdaptationMetadata) (v1.NodeID, error) {
	if id, ok, err := c.find(ctx, svc, caller, a.Provider, a.SourceID); err != nil {
		return "", err
	} else if ok {
		return id, nil
	}

	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: caller, MediaType: a.MediaType, Title: a.Title,
		ExternalIDs: encodeExternalIDs(map[string]string{a.Provider: a.SourceID}),
	})
	if err != nil {
		return "", fmt.Errorf("create adaptation source work: %w", err)
	}
	if _, err := svc.BindContentSource(ctx, v1.BindContentSourceCommand{
		Caller: caller, NodeID: work.Work.ID,
		SourceProvider: a.Provider, SourceRef: a.SourceID,
		MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact, Status: v1.BindingConfirmed,
	}); err != nil {
		return "", fmt.Errorf("bind adaptation source: %w", err)
	}
	return work.Work.ID, nil
}
