// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The candidate set behind one item, ranked and explained (platform#71).
//
// Selection picks a release and reports what it chose out of how many
// (platform#27), but a count alone cannot be acted on: a ranking that picked
// badly and an item that only ever had one candidate look identical from a
// screen.
//
// This returns the list rather than its length, in the order ranking would take
// them, each carrying why it sits where it does. The same list with nothing in
// it is the no-candidate state.

// PlaybackSourcesQuery asks what could be played for one item.
type PlaybackSourcesQuery struct {
	Caller v1.Caller
	// NodeID is the item. A source list belongs to the thing being watched rather
	// than to one of its releases: the use is asking "what else is there" from a
	// release the viewer wants to move away from.
	NodeID v1.NodeID
	// Prefer is the calling client's profile, so the ranking shown is the ranking
	// that would actually be used. Ranking a picker against a different profile
	// than the play would use makes the order advice the Platform then ignores.
	Prefer PlaybackPreference
}

// PlaybackSource is one candidate release, as a person would choose between
// them.
type PlaybackSource struct {
	PartID v1.PartID
	// Release is the source's own name for it, which is what a viewer actually
	// recognises — the group, the edition, the tag.
	Release string
	// Provider is who offered it.
	Provider string
	// Quality is the short summary a picker shows beside the name: resolution,
	// codecs, dynamic range, size.
	Quality string
	// Chosen marks the one ranking would take, so the picker can say which is
	// already in force rather than making a viewer infer it from the order.
	Chosen bool
	// Direct reports that this release needs no transcoding on this client — the
	// difference between a play that costs nothing and one that may not keep up.
	Direct bool
	// Why explains this candidate's standing in one phrase — what will have to
	// happen to play it, or that nothing will. Empty for a release with nothing
	// to say about it.
	Why string
}

// PlaybackSourcesResult is the ranked candidate set.
type PlaybackSourcesResult struct {
	Sources []PlaybackSource
	// Total is how many candidates exist before any were judged. It equals
	// len(Sources) today and is reported separately because they answer
	// different questions — "what can I pick" and "what does this item have" —
	// and a future filter would separate them.
	Total int
}

// PlaybackSources lists the candidate releases for an item, ranked for this
// client (platform#71).
//
// It ranks and must not resolve. Resolution is a call per candidate to an
// aggregator that takes hundreds of milliseconds, so resolving twenty to draw a
// list would spend the whole latency budget of a play on a screen the viewer may
// only be glancing at. Picking one is an ordinary play, and that is where its
// address is fetched.
func (s *Service) PlaybackSources(ctx context.Context, q PlaybackSourcesQuery) (PlaybackSourcesResult, error) {
	if q.Caller.Session == "" {
		return PlaybackSourcesResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.NodeID == "" {
		return PlaybackSourcesResult{}, contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return PlaybackSourcesResult{}, err
	}
	if s.parts == nil {
		return PlaybackSourcesResult{}, contracts.NewError(contracts.Unavailable, "no part store configured")
	}

	parts, err := s.parts.ListByNode(ctx, q.NodeID)
	if err != nil {
		return PlaybackSourcesResult{}, err
	}
	// Nothing playable is a legitimate answer and not an error: an item can be in
	// the library with no candidate at all, from a metadata-only import or a
	// source that has stopped offering it.
	if len(parts) == 0 {
		return PlaybackSourcesResult{}, nil
	}

	ranked := make([]PlaybackSource, 0, len(parts))
	scores := make(map[v1.PartID]int, len(parts))
	for _, p := range parts {
		scores[p.ID] = playbackScore(p, q.Prefer)
		direct, why := playability(p, q.Prefer)
		ranked = append(ranked, PlaybackSource{
			PartID: p.ID, Release: p.EditionLabel, Provider: p.Location.Provider,
			Quality: qualitySummary(p), Direct: direct, Why: why,
		})
	}

	// Highest score first, and the source's own order breaks a tie. The tiebreak
	// must stay stable: without one the list reorders between two renders of the
	// same screen, and a viewer reaching for the third row gets whatever landed
	// there this time.
	order := make(map[v1.PartID]float64, len(parts))
	for _, p := range parts {
		order[p.ID] = p.NaturalOrder
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		a, b := ranked[i].PartID, ranked[j].PartID
		if scores[a] != scores[b] {
			return scores[a] > scores[b]
		}
		return order[a] < order[b]
	})
	ranked[0].Chosen = true

	return PlaybackSourcesResult{Sources: ranked, Total: len(ranked)}, nil
}

// playability reports whether a release plays untouched on this client, and says
// what would have to happen if not.
//
// The phrasing is about this client rather than about the file: a release is not
// bad, it is undecodable by the thing asking, and the same release on a
// television may be the best answer there is. Every phrase names the work a play
// would cost, because that is what a viewer is choosing between.
//
// Codec comparisons lower-case the Part's codec before the lookup, so the
// preference sets must be keyed in lower case.
func playability(p v1.Part, prefer PlaybackPreference) (bool, string) {
	if prefer.Empty() {
		// Nothing declared, so nothing can be claimed. Silence rather than an
		// optimistic "plays directly".
		return false, ""
	}

	var problems []string
	if p.VideoCodec != "" && len(prefer.VideoCodecs) > 0 && !prefer.VideoCodecs[strings.ToLower(p.VideoCodec)] {
		problems = append(problems, "video re-encode ("+p.VideoCodec+")")
	}
	if p.HDRFormat != "" && !prefer.HDR {
		problems = append(problems, "tone-map from "+p.HDRFormat)
	}
	if p.AudioCodec != "" && len(prefer.AudioCodecs) > 0 && !prefer.AudioCodecs[strings.ToLower(p.AudioCodec)] {
		problems = append(problems, "audio re-encode ("+p.AudioCodec+")")
	}
	if prefer.MaxHeight > 0 && p.Height > prefer.MaxHeight {
		problems = append(problems, "larger than this screen")
	}
	if len(problems) == 0 {
		return true, "Plays directly"
	}
	return false, "Needs " + strings.Join(problems, ", ")
}

// qualitySummary is the short line under a release's name.
//
// Resolution, codecs and size, in the order somebody scanning a list reads them.
// Missing fields are omitted rather than rendered as "unknown": a module's parse
// is best-effort (platform#27), so an absent codec is ordinary.
func qualitySummary(p v1.Part) string {
	var parts []string
	if p.Height > 0 {
		parts = append(parts, strconv.Itoa(p.Height)+"p")
	}
	if p.HDRFormat != "" {
		parts = append(parts, p.HDRFormat)
	}
	if p.VideoCodec != "" {
		parts = append(parts, p.VideoCodec)
	}
	if p.AudioCodec != "" {
		parts = append(parts, p.AudioCodec)
	}
	if p.SizeBytes > 0 {
		parts = append(parts, humanSize(p.SizeBytes))
	}
	return strings.Join(parts, " · ")
}

// humanSize renders a byte count the way a release is usually described.
func humanSize(b int64) string {
	const gb = 1 << 30
	switch {
	case b >= gb:
		return strconv.FormatFloat(float64(b)/gb, 'f', 1, 64) + " GB"
	case b >= 1<<20:
		return strconv.FormatInt(b/(1<<20), 10) + " MB"
	default:
		return strconv.FormatInt(b, 10) + " B"
	}
}

// PlayableAfterImportQuery asks which release to play for a work that has just
// been materialised (platform#73).
type PlayableAfterImportQuery struct {
	Caller v1.Caller
	WorkID v1.NodeID
}

// PlayableAfterImportResult names the one item worth playing, when there is
// exactly one.
type PlayableAfterImportResult struct {
	NodeID v1.NodeID
	PartID v1.PartID
	// Ambiguous reports that the work has more than one playable item — a series.
	// It is a distinct answer rather than an error because it is the correct
	// outcome: adding a series and guessing an episode is worse than adding it
	// and letting the viewer choose.
	Ambiguous bool
}

// PlayableAfterImport finds the release to start, for a work materialised by a
// play (platform#73).
//
// A film is one item and a series is many, which is the whole function.
// Materialising a film from a Play button should start it; doing the same for a
// series would have to guess an episode, so the answer there is to have added
// the series and re-drawn the screen with its episodes on it.
func (s *Service) PlayableAfterImport(ctx context.Context, q PlayableAfterImportQuery) (PlayableAfterImportResult, error) {
	if q.Caller.Session == "" {
		return PlayableAfterImportResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.WorkID == "" {
		return PlayableAfterImportResult{}, contracts.NewError(contracts.InvalidArgument, "work id is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return PlayableAfterImportResult{}, err
	}
	if s.nodes == nil || s.parts == nil {
		return PlayableAfterImportResult{}, contracts.NewError(contracts.Unavailable, "no content stores configured")
	}

	nodes, err := s.nodes.ListByWork(ctx, q.WorkID)
	if err != nil {
		return PlayableAfterImportResult{}, err
	}
	_, _, items := indexWork(q.WorkID, nodes)

	var found PlayableAfterImportResult
	for _, item := range items {
		parts, err := s.parts.ListByNode(ctx, item.ID)
		if err != nil || len(parts) == 0 {
			continue
		}
		if found.PartID != "" {
			// A second playable item. Nothing here can choose between episodes,
			// and a wrong guess starts the wrong one.
			return PlayableAfterImportResult{Ambiguous: true}, nil
		}
		found = PlayableAfterImportResult{NodeID: item.ID, PartID: parts[0].ID}
	}
	return found, nil
}
