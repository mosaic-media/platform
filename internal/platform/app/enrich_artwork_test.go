// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"strings"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The selection rule (ADR 0074) is where "better artwork" actually happens, and
// every way it can be wrong is silent: a worse image is still an image, so
// nothing fails and nobody is told. These cover the rules the record states.

// TestMergeArtworkPrefersTextlessBackdrops is the rule most visible to a viewer.
// A backdrop with the title burned into it sits behind a hero that draws its own
// clearlogo on top, so the two collide — and a rule that only counted votes
// would pick the collision whenever it was the more popular image, which it
// usually is.
func TestMergeArtworkPrefersTextlessBackdrops(t *testing.T) {
	got := mergeArtwork(v1.Artwork{}, []v1.ArtworkCandidate{
		{Slot: v1.ArtworkBackdrop, URL: "with-title", Language: "en", Rank: 900},
		{Slot: v1.ArtworkBackdrop, URL: "textless", Language: "", Rank: 5},
	})

	if got.Backdrop != "textless" {
		t.Errorf("Backdrop = %q, want the textless one even though it is far less popular", got.Backdrop)
	}
	// The popular one is kept as a candidate — this is a preference, not a
	// filter, and a user must still be able to choose it.
	if len(got.CandidatesFor(v1.ArtworkBackdrop)) != 2 {
		t.Error("the rejected backdrop should survive as a candidate")
	}
}

// TestMergeArtworkDoesNotPreferTextlessPosters is the counter-case, and the
// reason textless is a per-slot rule rather than a blanket one. A poster's
// typography is part of the artwork; preferring a textless poster would
// systematically pick the worse image.
func TestMergeArtworkDoesNotPreferTextlessPosters(t *testing.T) {
	got := mergeArtwork(v1.Artwork{}, []v1.ArtworkCandidate{
		{Slot: v1.ArtworkPoster, URL: "textless", Language: "", Rank: 5},
		{Slot: v1.ArtworkPoster, URL: "proper-poster", Language: "en", Rank: 900},
	})

	if got.Poster != "proper-poster" {
		t.Errorf("Poster = %q, want the higher-ranked poster — text belongs on a poster", got.Poster)
	}
}

// TestMergeArtworkKeepsTheExistingArtAsACandidate covers the rule that keeps a
// deployment from going backwards. The node's art came from the module that
// materialised it; an artwork provider that has nothing for this title must not
// leave the node worse off than if it had never been installed.
func TestMergeArtworkKeepsTheExistingArtAsACandidate(t *testing.T) {
	existing := v1.Artwork{Poster: "from-metadata-module", Logo: "metadata-logo"}

	// A provider that answered about a different slot entirely.
	got := mergeArtwork(existing, []v1.ArtworkCandidate{
		{Slot: v1.ArtworkBanner, URL: "banner", Source: "fanart-tv"},
	})

	if got.Poster != "from-metadata-module" {
		t.Errorf("Poster = %q, want the existing art preserved", got.Poster)
	}
	if got.Logo != "metadata-logo" {
		t.Errorf("Logo = %q, want the existing art preserved", got.Logo)
	}
	if got.Slot(v1.ArtworkBanner) != "banner" {
		t.Error("the new banner should be reachable through the candidate set")
	}
}

// TestMergeArtworkIsIdempotent is what makes the pass safe to re-run. Import is
// idempotent by design, so enrichment runs again on every re-import and folds
// the same candidates into a set that already holds them. Without dedup the
// document would double in size every time a user re-added a title — a fault
// that grows silently and only shows up as a slow library.
func TestMergeArtworkIsIdempotent(t *testing.T) {
	gathered := []v1.ArtworkCandidate{
		{Slot: v1.ArtworkPoster, URL: "a", Source: "fanart-tv", Rank: 10},
		{Slot: v1.ArtworkBackdrop, URL: "b", Source: "fanart-tv", Rank: 20},
	}

	once := mergeArtwork(v1.Artwork{}, gathered)
	twice := mergeArtwork(once, gathered)

	if len(once.Candidates) != len(twice.Candidates) {
		t.Errorf("candidates grew from %d to %d on a second pass", len(once.Candidates), len(twice.Candidates))
	}
	if once.Poster != twice.Poster || once.Backdrop != twice.Backdrop {
		t.Error("a second pass changed the selection; enrichment must converge")
	}
}

// TestMergeArtworkCapsCandidatesPerSlot covers the bound ADR 0074 names in the
// design rather than leaving to be discovered. The document is read on every
// list render, so an unbounded set makes every rail in the library pay for a
// picker's completeness.
func TestMergeArtworkCapsCandidatesPerSlot(t *testing.T) {
	var gathered []v1.ArtworkCandidate
	for i := range 40 {
		gathered = append(gathered, v1.ArtworkCandidate{
			Slot: v1.ArtworkPoster, URL: string(rune('a'+i%26)) + strings.Repeat("x", i), Rank: float64(i),
		})
	}

	got := mergeArtwork(v1.Artwork{}, gathered)

	posters := got.CandidatesFor(v1.ArtworkPoster)
	if len(posters) != artworkCandidateCap {
		t.Errorf("kept %d posters, want the cap of %d", len(posters), artworkCandidateCap)
	}
	// The cap keeps the best, not an arbitrary window — truncating the head
	// would throw away exactly the candidates the rule just ranked highest.
	if posters[0].Rank != 39 {
		t.Errorf("best kept poster has rank %v, want the highest (39)", posters[0].Rank)
	}
}

// TestMergeArtworkIsDeterministicAcrossSources covers the tie-break. Ranks are
// not comparable across sources (see v1.ArtworkCandidate.Rank), so when two
// providers offer the same slot the order must still be stable — otherwise the
// same import twice picks different art and nothing explains why.
func TestMergeArtworkIsDeterministicAcrossSources(t *testing.T) {
	gathered := []v1.ArtworkCandidate{
		{Slot: v1.ArtworkLogo, URL: "from-tmdb", Source: "tmdb", Rank: 7},
		{Slot: v1.ArtworkLogo, URL: "from-fanart", Source: "fanart-tv", Rank: 7},
	}

	first := mergeArtwork(v1.Artwork{}, gathered)
	second := mergeArtwork(v1.Artwork{}, gathered)

	if first.Logo != second.Logo {
		t.Errorf("selection is not deterministic: %q then %q", first.Logo, second.Logo)
	}
	// Stable module-id order is the stated tie-break, so the earlier id wins.
	if first.Logo != "from-fanart" {
		t.Errorf("Logo = %q, want the lower module id to break the tie", first.Logo)
	}
}

// TestMergeArtworkSlotsWithNoFlatFieldStayReachable covers the shape choice in
// ADR 0074: only four slots get a flat field, and everything else lives in the
// candidate set rather than growing the struct a field per art type.
func TestMergeArtworkSlotsWithNoFlatFieldStayReachable(t *testing.T) {
	got := mergeArtwork(v1.Artwork{}, []v1.ArtworkCandidate{
		{Slot: v1.ArtworkClearArt, URL: "clearart", Source: "fanart-tv"},
		{Slot: v1.ArtworkDisc, URL: "disc", Source: "fanart-tv"},
	})

	if got.Slot(v1.ArtworkClearArt) != "clearart" {
		t.Error("clearart should resolve through the candidate set")
	}
	if got.Slot(v1.ArtworkDisc) != "disc" {
		t.Error("disc art should resolve through the candidate set")
	}
	// And it did not quietly become the poster, which is the failure mode of
	// mapping an unrecognised slot onto a known one.
	if got.Poster != "" {
		t.Errorf("Poster = %q, want empty — nothing supplied a poster", got.Poster)
	}
}
