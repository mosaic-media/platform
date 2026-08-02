// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The consumer for the `subtitles` capability role (ADR 0117).
//
// **The role has been fillable since ADR 0037 and nothing ever asked it.** Two
// modules implement it, the registry could resolve one *by name*, and no code
// path anywhere knew a name to ask for — so the enumerator that would have made
// it reachable did not exist either. That is the shape of the gap: not a missing
// feature but a missing call.
//
// It is asked at play time rather than at import, and that is the substantive
// difference from stream enrichment. A subtitle file is small, external and
// perishable in the same way a debrid link is; resolving twenty of them into the
// graph at import buys entries that have been decaying since before anyone
// wanted them, which is the mistake [ADR 0049](0049) already names for streams.

// PlaybackSubtitlesQuery asks the installed subtitle sources what they have for
// one item.
type PlaybackSubtitlesQuery struct {
	Caller v1.Caller
	// NodeID is the item being played. Season and episode are derived from the
	// graph rather than taken from the caller, so a client cannot ask for one
	// episode's subtitles under another's identity.
	NodeID v1.NodeID
}

// ExternalSubtitle is one track a module resolved: a file somewhere else.
type ExternalSubtitle struct {
	// Language is the code or display name the source labelled it with. It is
	// **not** normalised here: a source's own label is what a viewer recognises
	// in a menu, and mapping "Brazilian Portuguese" onto "por" loses the only
	// distinction that made two entries tellable apart.
	Language string
	// URL is where the file is. It never reaches a client — the origin fetches
	// it (ADR 0045: a module resolves, the Platform serves).
	URL string
	// ModuleID is who offered it, for telling two sources' answers apart.
	ModuleID string
}

// PlaybackSubtitlesResult is what every installed source had, best-first within
// each source, in stable module order.
type PlaybackSubtitlesResult struct {
	Subtitles []ExternalSubtitle
}

// PlaybackSubtitles asks every installed subtitles provider for this item
// (ADR 0117).
//
// **Best-effort by construction, exactly like stream enrichment.** Every failure
// here logs and continues: a subtitle source that is down, unconfigured or
// simply does not know the title must not fail a play. The worst outcome is a
// playback with the subtitles the release already carried, which is what
// happened before this existed at all.
func (s *Service) PlaybackSubtitles(ctx context.Context, q PlaybackSubtitlesQuery) (PlaybackSubtitlesResult, error) {
	if q.Caller.Session == "" {
		return PlaybackSubtitlesResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.NodeID == "" {
		return PlaybackSubtitlesResult{}, contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return PlaybackSubtitlesResult{}, err
	}

	providers := s.capabilities.SubtitlesProviders()
	if len(providers) == 0 || s.nodes == nil {
		return PlaybackSubtitlesResult{}, nil
	}

	item, err := s.nodes.FindByID(ctx, q.NodeID)
	if err != nil {
		return PlaybackSubtitlesResult{}, err
	}
	work, byID, err := s.workContaining(ctx, item)
	if err != nil {
		// Not fatal. An item whose work cannot be read still plays; it plays
		// without the subtitles a source might have had.
		telemetry.From(ctx).Warn("subtitle resolution could not read the work",
			telemetry.String("node_id", string(q.NodeID)), telemetry.Err(err))
		return PlaybackSubtitlesResult{}, nil
	}

	// The same shared identities stream enrichment uses, and for the same reason
	// (ADR 0073): a subtitles provider is asked about content it did not source,
	// so it is handed a neutral external id rather than a native one it could
	// not have.
	identities := sharedIdentitiesOf(work)
	if len(identities) == 0 {
		return PlaybackSubtitlesResult{}, nil
	}
	season, episode := coordinatesOf(item, byID)

	var out []ExternalSubtitle
	seen := map[string]bool{}
	for _, provider := range providers {
		settings, err := s.readModuleSettings(ctx, provider.ModuleID)
		if err != nil {
			telemetry.From(ctx).Warn("subtitle provider settings could not be read",
				telemetry.String("module", provider.ModuleID), telemetry.Err(err))
			continue
		}
		for _, identity := range identities {
			ref := v1.ContentRef{
				Provider:       provider.ModuleID,
				MediaType:      work.MediaType,
				ExternalScheme: identity.Scheme,
				ExternalID:     identity.ID,
			}
			mctx, span := moduleSpan(ctx, provider.ModuleID, "subtitles")
			resp, err := provider.Provider.Subtitles(mctx, v1.SubtitlesRequest{
				Caller: q.Caller, Settings: settings, Ref: ref,
				Season: season, Episode: episode,
			})
			failSpan(span, err)
			span.End()
			if err != nil {
				telemetry.From(ctx).Warn("subtitle provider failed",
					telemetry.String("module", provider.ModuleID),
					telemetry.String("scheme", identity.Scheme), telemetry.Err(err))
				continue
			}
			for _, sub := range resp.Subtitles {
				if sub.URL == "" || seen[sub.URL] {
					// The same file under two of the work's identities is one
					// track, not two. Without this a title with an IMDb id and a
					// TMDB id lists every subtitle twice.
					continue
				}
				seen[sub.URL] = true
				out = append(out, ExternalSubtitle{
					Language: strings.TrimSpace(sub.Language),
					URL:      sub.URL,
					ModuleID: provider.ModuleID,
				})
			}
			if len(resp.Subtitles) > 0 {
				// One identity answering is enough from this provider. Asking the
				// rest would re-ask the same source about the same title under a
				// different name for it.
				break
			}
		}
	}

	return PlaybackSubtitlesResult{Subtitles: out}, nil
}

// workContaining finds the work an item belongs to, and the sibling index the
// episode coordinates are derived from.
//
// An item that *is* a work — a film — is its own work, which is why this is not
// simply a parent lookup.
func (s *Service) workContaining(ctx context.Context, item v1.Node) (v1.Node, map[v1.NodeID]v1.Node, error) {
	workID := item.WorkID
	if workID == "" {
		workID = item.ID
	}
	nodes, err := s.nodes.ListByWork(ctx, workID)
	if err != nil {
		return v1.Node{}, nil, err
	}
	work, byID, _ := indexWork(workID, nodes)
	return work, byID, nil
}
