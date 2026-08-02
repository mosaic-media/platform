// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Choosing what to start after a play materialised something (ADR 0118).

// TestAFilmStartsAndASeriesDoesNot is the whole distinction. A film is one item,
// so playing it is unambiguous; a series is many, and guessing an episode is
// worse than adding it and drawing the ones that now exist.
func TestAFilmStartsAndASeriesDoesNot(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	// A film: one item under the work, with a release.
	db.seedNode(v1.Node{ID: "film-work", WorkID: "film-work", Kind: v1.NodeWork, MediaType: v1.MediaMovie})
	db.seedNode(v1.Node{ID: "film-item", WorkID: "film-work", Kind: v1.NodeItem, MediaType: v1.MediaMovie})
	seedParts(db, []v1.Part{{ID: "film-part", NodeID: "film-item", NaturalOrder: 1}})

	got, err := svc.PlayableAfterImport(ctx, app.PlayableAfterImportQuery{Caller: caller, WorkID: "film-work"})
	if err != nil {
		t.Fatalf("PlayableAfterImport: %v", err)
	}
	if got.Ambiguous {
		t.Fatal("a film reported as ambiguous; there is only one thing it could mean")
	}
	if got.PartID != "film-part" || got.NodeID != "film-item" {
		t.Errorf("got %+v, want the film's own release", got)
	}

	// A series: two playable episodes, so nothing here can choose.
	db.seedNode(v1.Node{ID: "show-work", WorkID: "show-work", Kind: v1.NodeWork, MediaType: v1.MediaTVSeries})
	db.seedNode(v1.Node{ID: "ep-1", WorkID: "show-work", Kind: v1.NodeItem, MediaType: v1.MediaTVSeries})
	db.seedNode(v1.Node{ID: "ep-2", WorkID: "show-work", Kind: v1.NodeItem, MediaType: v1.MediaTVSeries})
	seedParts(db, []v1.Part{
		{ID: "ep-1-part", NodeID: "ep-1", NaturalOrder: 1},
		{ID: "ep-2-part", NodeID: "ep-2", NaturalOrder: 2},
	})

	got, err = svc.PlayableAfterImport(ctx, app.PlayableAfterImportQuery{Caller: caller, WorkID: "show-work"})
	if err != nil {
		t.Fatalf("PlayableAfterImport(series): %v", err)
	}
	if !got.Ambiguous {
		t.Errorf("a series resolved to %+v; starting a guessed episode is worse than starting nothing", got)
	}
	if got.PartID != "" {
		t.Errorf("an ambiguous answer still named %q", got.PartID)
	}
}

// TestNothingPlayableIsNotAnError covers the case where the import succeeded and
// found no releases — a metadata-only source, or nothing installed that offers
// files. The item is in the library, which is a real outcome; there is simply
// nothing to start.
func TestNothingPlayableIsNotAnError(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	db.seedNode(v1.Node{ID: "bare-work", WorkID: "bare-work", Kind: v1.NodeWork, MediaType: v1.MediaMovie})
	db.seedNode(v1.Node{ID: "bare-item", WorkID: "bare-work", Kind: v1.NodeItem, MediaType: v1.MediaMovie})

	got, err := svc.PlayableAfterImport(ctx, app.PlayableAfterImportQuery{
		Caller: v1.Caller{Session: string(session)}, WorkID: "bare-work",
	})
	if err != nil {
		t.Fatalf("an added item with no releases must not be an error: %v", err)
	}
	if got.PartID != "" || got.Ambiguous {
		t.Errorf("got %+v, want nothing to start and nothing ambiguous", got)
	}
}
