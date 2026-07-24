// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"sort"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Artwork enrichment (ADR 0075), and the selection rule (ADR 0074).
//
// Artwork used to arrive as a by-product of asking a question about *titles*:
// whichever module described the content supplied whatever art it happened to
// carry. A dedicated artwork database is a different kind of source — it has no
// titles, no search and no catalogs, so it is never named by a ref and no
// existing role fits it. Without a pass like this one it would sit registered
// and never be asked about the title it has the best art for.
//
// It is the same shape as enrichStreams and differs in one way that matters:
// **it does not stop at the first provider that answers.** Candidates from
// several sources union into one set rather than competing for a slot, because
// ADR 0074 makes them additive and attributable — so there is no first-wins rule
// here and no cross-provider dedup problem left open.

// artworkCandidateCap bounds how many candidates are kept per slot per node.
//
// ADR 0074 names the cap in the design rather than leaving it to be discovered:
// a real artwork database returns dozens of posters per title, the document is
// read on every list render, and an unbounded set would grow the hot path of
// every rail in the library to make a picker marginally more complete. Twelve is
// enough to choose from and small enough not to notice.
const artworkCandidateCap = 12

// enrichArtwork resolves artwork for a materialised work and its seasons.
//
// **Best-effort by construction**, exactly as the stream pass is: every failure
// path logs and continues. A provider that is unreachable, unconfigured, or
// simply does not know the title must not lose a user the work they just added —
// keeping the art the metadata module already supplied is a correct outcome, and
// it is what a deployment with no artwork provider produces anyway.
func (s *Service) enrichArtwork(ctx context.Context, caller v1.Caller, workID v1.NodeID) {
	providers := s.capabilities.ArtworkProviders()
	if len(providers) == 0 {
		return
	}

	nodes, err := s.nodes.ListByWork(ctx, workID)
	if err != nil {
		telemetry.From(ctx).Warn("artwork enrichment could not read the work",
			telemetry.String("work_id", string(workID)), telemetry.Err(err))
		return
	}

	work, _, _ := indexWork(workID, nodes)

	// The identities an artwork provider might recognise, read from the work
	// rather than from the ref: a provider that did not source this content holds
	// no id of its own for it (ADR 0073).
	identities := sharedIdentitiesOf(work)
	if len(identities) == 0 {
		// A source keyed only to itself is unenrichable by design rather than by
		// oversight. Cinemeta binds only `imdb`, which is enough for films and
		// not for television at a source that keys series on TVDB — a real and
		// visible limit, recorded in ADR 0075 rather than papered over.
		return
	}

	// The work itself, then each season container. Episodes are deliberately not
	// enriched: a metadata provider already returns every episode still in one
	// call, and asking an artwork database per episode would be a request per
	// episode for data the Platform has in bulk.
	targets := []struct {
		node   v1.Node
		season int
	}{{node: work}}
	for _, node := range nodes {
		if node.Kind == v1.NodeContainer && node.ContainerType == v1.ContainerSeason {
			targets = append(targets, struct {
				node   v1.Node
				season int
			}{node: node, season: int(node.NaturalOrder)})
		}
	}

	for _, target := range targets {
		var gathered []v1.ArtworkCandidate

		for _, provider := range providers {
			settings, err := s.readModuleSettings(ctx, provider.ModuleID)
			if err != nil {
				telemetry.From(ctx).Warn("artwork enrichment could not read module settings",
					telemetry.String("module", provider.ModuleID), telemetry.Err(err))
				continue
			}

			mctx, span := moduleSpan(ctx, provider.ModuleID, "artwork")
			resp, err := provider.Provider.Artwork(mctx, v1.ArtworkRequest{
				Caller: caller, Settings: settings,
				Identities: identities,
				MediaType:  work.MediaType,
				Season:     target.season,
			})
			failSpan(span, err)
			span.End()
			if err != nil {
				telemetry.From(ctx).Warn("artwork provider failed during enrichment",
					telemetry.String("module", provider.ModuleID), telemetry.Err(err))
				continue
			}

			// Provenance is stamped here rather than trusted from the response:
			// the Platform knows which module it just called, and the module is
			// the one party that could get its own id wrong.
			for _, candidate := range resp.Candidates {
				if candidate.Slot == "" || candidate.URL == "" {
					continue
				}
				candidate.Source = provider.ModuleID
				gathered = append(gathered, candidate)
			}
		}

		if len(gathered) == 0 {
			continue
		}

		merged := mergeArtwork(target.node.Artwork, gathered)
		if _, err := s.SetContentArtwork(ctx, v1.SetContentArtworkCommand{
			Caller: caller, NodeID: target.node.ID, Artwork: merged,
		}); err != nil {
			telemetry.From(ctx).Warn("artwork enrichment could not store the resolved artwork",
				telemetry.String("node_id", string(target.node.ID)), telemetry.Err(err))
		}
	}
}

// mergeArtwork folds newly-gathered candidates into what a node already has and
// resolves the flat slots from the result.
//
// The node's existing art comes from the module that materialised it, and it is
// kept as a candidate rather than discarded: a metadata provider's poster is a
// legitimate option, and a deployment whose artwork source has nothing for a
// title must not end up worse off than one with no artwork source at all.
func mergeArtwork(existing v1.Artwork, gathered []v1.ArtworkCandidate) v1.Artwork {
	candidates := append([]v1.ArtworkCandidate{}, existing.Candidates...)

	// The existing selection, promoted to a candidate so it can be chosen again.
	// Rank stays zero — a metadata module supplies no ranking, and inventing one
	// would put it above candidates whose rank is a real vote count.
	for slot, url := range map[v1.ArtworkSlot]string{
		v1.ArtworkPoster:    existing.Poster,
		v1.ArtworkLandscape: existing.Landscape,
		v1.ArtworkBackdrop:  existing.Backdrop,
		v1.ArtworkLogo:      existing.Logo,
	} {
		if url != "" {
			candidates = append(candidates, v1.ArtworkCandidate{Slot: slot, URL: url})
		}
	}
	candidates = append(candidates, gathered...)

	candidates = dedupeArtworkCandidates(candidates)
	candidates = rankArtworkCandidates(candidates)

	out := v1.Artwork{Candidates: candidates}
	out.Poster = firstURLFor(candidates, v1.ArtworkPoster)
	out.Landscape = firstURLFor(candidates, v1.ArtworkLandscape)
	out.Backdrop = firstURLFor(candidates, v1.ArtworkBackdrop)
	out.Logo = firstURLFor(candidates, v1.ArtworkLogo)
	return out
}

// dedupeArtworkCandidates removes candidates repeating a URL already seen for
// the same slot, keeping the first.
//
// It matters because the pass is idempotent: a re-import gathers the same
// candidates again and folds them into a set that already holds them, and
// without this every re-import would double the document. Keeping the first
// preserves the richer entry, since the existing selection is promoted before
// the freshly gathered ones are appended.
func dedupeArtworkCandidates(candidates []v1.ArtworkCandidate) []v1.ArtworkCandidate {
	type key struct {
		slot v1.ArtworkSlot
		url  string
	}
	seen := make(map[key]bool, len(candidates))
	out := make([]v1.ArtworkCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		k := key{slot: candidate.Slot, url: candidate.URL}
		if seen[k] {
			continue
		}
		seen[k] = true
		out = append(out, candidate)
	}
	return out
}

// rankArtworkCandidates sorts the set best-first within each slot and applies
// the per-slot cap.
//
// **This is ADR 0074's selection rule, and it is deliberately boring.** The
// point of the candidate set is having somewhere to record a *choice*; the rule
// is what fills the slot until a user makes one. It prefers, in order:
//
//  1. **Textless, for the slots that sit under something.** A backdrop or
//     landscape with a title burned into it is wrong behind a hero that draws its
//     own clearlogo on top — this is the single change most visible to a viewer,
//     and it is invisible to a rule that only counts votes.
//  2. **The source's own rank**, which is a vote count where a source has one.
//     Ranks are not compared across sources because they are not comparable
//     (see v1.ArtworkCandidate.Rank); they order within one source's entries and
//     the tie-break below settles the rest.
//  3. **Provider order**, which is stable module-id order — an arbitrary but
//     fixed answer, so the same import twice produces the same selection.
//
// The sort is stable, so candidates equal under every rule keep the order they
// were gathered in: the node's existing art before anything a provider added.
func rankArtworkCandidates(candidates []v1.ArtworkCandidate) []v1.ArtworkCandidate {
	sort.SliceStable(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		if a.Slot != b.Slot {
			return a.Slot < b.Slot
		}
		if prefersTextless(a.Slot) {
			at, bt := a.Language == "", b.Language == ""
			if at != bt {
				return at
			}
		}
		if a.Rank != b.Rank {
			return a.Rank > b.Rank
		}
		return a.Source < b.Source
	})

	perSlot := make(map[v1.ArtworkSlot]int)
	out := make([]v1.ArtworkCandidate, 0, len(candidates))
	for _, candidate := range candidates {
		if perSlot[candidate.Slot] >= artworkCandidateCap {
			continue
		}
		perSlot[candidate.Slot]++
		out = append(out, candidate)
	}
	return out
}

// prefersTextless reports whether a slot is one that sits *under* other
// elements, where burned-in text is a defect rather than a variant.
//
// A poster is the counter-case and the reason this is not a blanket rule: a
// poster's typography is part of the artwork, and preferring a textless one
// would systematically choose the worse image.
func prefersTextless(slot v1.ArtworkSlot) bool {
	return slot == v1.ArtworkBackdrop || slot == v1.ArtworkLandscape
}

// firstURLFor returns the best candidate's URL for one slot, the set being
// sorted best-first by the time this is called.
func firstURLFor(candidates []v1.ArtworkCandidate, slot v1.ArtworkSlot) string {
	for _, candidate := range candidates {
		if candidate.Slot == slot {
			return candidate.URL
		}
	}
	return ""
}
