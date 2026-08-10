// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The candidate set, ranked and explained (platform#71).

// seedParts puts candidate releases in the fake store directly. The public path
// to one is a materialisation, which is several commands of setup for a fixture
// that only needs the store's contents to be a known list.
func seedParts(db *fakeDB, parts []v1.Part) {
	db.mu.Lock()
	defer db.mu.Unlock()
	for _, p := range parts {
		db.parts[p.ID] = p
	}
}

// browserPrefer is a client that decodes h264 and AAC and renders no HDR, which
// is what makes the difference between candidates visible.
func browserPrefer() app.PlaybackPreference {
	return app.PlaybackPreference{
		VideoCodecs: map[string]bool{"h264": true},
		AudioCodecs: map[string]bool{"aac": true},
	}
}

// TestTheListIsRankedAndTheChosenOneIsNamed is the property the whole slice
// exists for. Selection already picked; what it could not do was show which and
// why, so two very different situations — a bad ranking and a single candidate —
// looked identical from a screen.
func TestTheListIsRankedAndTheChosenOneIsNamed(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)

	seedParts(db, []v1.Part{
		{ID: "p-hevc", NodeID: "node-1", EditionLabel: "4K HDR", VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160, HDRFormat: "HDR10", NaturalOrder: 1},
		{ID: "p-h264", NodeID: "node-1", EditionLabel: "1080p", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, NaturalOrder: 2},
	})

	res, err := svc.PlaybackSources(ctx, app.PlaybackSourcesQuery{
		Caller: v1.Caller{Session: string(session)}, NodeID: "node-1", Prefer: browserPrefer(),
	})
	if err != nil {
		t.Fatalf("PlaybackSources: %v", err)
	}
	if res.Total != 2 || len(res.Sources) != 2 {
		t.Fatalf("got %d sources (total %d), want 2", len(res.Sources), res.Total)
	}

	// The playable one first, however the source ordered them: compatibility
	// dominates resolution, because an unplayable 4K release is worth less than
	// a playable 1080p one.
	if res.Sources[0].PartID != "p-h264" {
		t.Errorf("first source = %q, want the one that plays on this client", res.Sources[0].PartID)
	}
	if !res.Sources[0].Chosen {
		t.Error("the top source is not marked chosen, so a viewer cannot tell which is in force")
	}
	if res.Sources[1].Chosen {
		t.Error("two sources marked chosen; exactly one is in force")
	}
	if !res.Sources[0].Direct || res.Sources[0].Why != "Plays directly" {
		t.Errorf("h264/aac reported as %q direct=%v, want a direct play", res.Sources[0].Why, res.Sources[0].Direct)
	}

	// And the expensive one says what it would cost, in terms of the work rather
	// than as a verdict on the file.
	why := res.Sources[1].Why
	for _, want := range []string{"video re-encode", "tone-map", "audio re-encode"} {
		if !strings.Contains(why, want) {
			t.Errorf("4K HDR source says %q, missing %q", why, want)
		}
	}
	if res.Sources[1].Direct {
		t.Error("a release needing three transforms is reported as a direct play")
	}
}

// TestNothingPlayableIsAnAnswerNotAnError is the other half of the same problem.
// An item with no candidate is ordinary — a metadata-only import, or a source
// that stopped offering it — and presenting it as a failure is what sent people
// looking for a bug in playback.
func TestNothingPlayableIsAnAnswerNotAnError(t *testing.T) {
	ctx := context.Background()
	svc, _, _, session := importFixture(t)

	res, err := svc.PlaybackSources(ctx, app.PlaybackSourcesQuery{
		Caller: v1.Caller{Session: string(session)}, NodeID: "node-empty",
	})
	if err != nil {
		t.Fatalf("an item with no candidates must not be an error: %v", err)
	}
	if res.Total != 0 || len(res.Sources) != 0 {
		t.Errorf("got %+v, want an empty set", res)
	}
}

// TestTheOrderIsStableAcrossRenders guards a bug a viewer would experience as
// the list moving under their finger. Two candidates that rank equally must come
// back in the same order every time, or the third row is whatever landed there
// on this render.
func TestTheOrderIsStableAcrossRenders(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)

	// Identical in everything the ranking looks at, so only the tiebreak decides.
	seedParts(db, []v1.Part{
		{ID: "p-b", NodeID: "node-1", EditionLabel: "B", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, NaturalOrder: 2},
		{ID: "p-a", NodeID: "node-1", EditionLabel: "A", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, NaturalOrder: 1},
		{ID: "p-c", NodeID: "node-1", EditionLabel: "C", VideoCodec: "h264", AudioCodec: "aac", Height: 1080, NaturalOrder: 3},
	})

	var first []v1.PartID
	for range 3 {
		res, err := svc.PlaybackSources(ctx, app.PlaybackSourcesQuery{
			Caller: v1.Caller{Session: string(session)}, NodeID: "node-1", Prefer: browserPrefer(),
		})
		if err != nil {
			t.Fatalf("PlaybackSources: %v", err)
		}
		got := make([]v1.PartID, 0, len(res.Sources))
		for _, s := range res.Sources {
			got = append(got, s.PartID)
		}
		if first == nil {
			first = got
			continue
		}
		for i := range got {
			if got[i] != first[i] {
				t.Fatalf("order changed between renders: %v then %v", first, got)
			}
		}
	}
	// The source's own order is the tiebreak, so A before B before C.
	if len(first) != 3 || first[0] != "p-a" || first[2] != "p-c" {
		t.Errorf("order = %v, want the source's own order as the tiebreak", first)
	}
}

// TestAnUndeclaredClientClaimsNothing is the honesty bound. With no profile
// there is no basis for saying a release plays directly, and an optimistic
// "Plays directly" on every row would be worse than silence.
func TestAnUndeclaredClientClaimsNothing(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	seedParts(db, []v1.Part{
		{ID: "p-1", NodeID: "node-1", EditionLabel: "Something", VideoCodec: "hevc", Height: 2160, NaturalOrder: 1},
	})

	res, err := svc.PlaybackSources(ctx, app.PlaybackSourcesQuery{
		Caller: v1.Caller{Session: string(session)}, NodeID: "node-1",
	})
	if err != nil {
		t.Fatalf("PlaybackSources: %v", err)
	}
	if len(res.Sources) != 1 {
		t.Fatalf("got %d sources, want 1", len(res.Sources))
	}
	if res.Sources[0].Direct || res.Sources[0].Why != "" {
		t.Errorf("claimed %q direct=%v against a client that declared nothing",
			res.Sources[0].Why, res.Sources[0].Direct)
	}
	// The quality summary still says what the release is, because that is a
	// fact about the file rather than a claim about the client.
	if !strings.Contains(res.Sources[0].Quality, "2160p") {
		t.Errorf("quality = %q, want the release's own facts", res.Sources[0].Quality)
	}
}
