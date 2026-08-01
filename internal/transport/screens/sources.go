// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"strconv"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The source picker, and the honest answer when there is nothing to pick
// (ADR 0116).
//
// **`SourcePicker` has been in the contract the whole time and nothing emitted
// it.** That is the shape this screen fixes: a definition exists, a client can
// render it, and no server surface ever sent one — so selection's answer was
// visible only as a number in a log. The screen is composed entirely from the
// vocabulary that already existed, which is the rule working rather than a
// coincidence.

// sourcesScreen lists what could be played for one item, ranked for this client.
func (s *Service) sourcesScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nodeID := stringParam(params, paramNodeID)
	if nodeID == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "sources screen needs a nodeId param")
	}
	title := stringParam(params, "title")

	res, err := s.content.PlaybackSources(ctx, app.PlaybackSourcesQuery{
		Caller: caller, NodeID: v1.NodeID(nodeID),
	})
	if err != nil {
		return nil, err
	}

	var body ui.El
	if len(res.Sources) == 0 {
		body = noSources()
	} else {
		body = ui.SourcePicker(ui.Sources(sourceRows(res.Sources, nodeID, title, params)))
	}

	return ui.Stack("vertical", 4,
		ui.Text(ui.Prop("text", headingFor(title)), ui.Prop("variant", "title")),
		ui.Text(ui.Prop("text", summaryFor(res)), ui.Prop("variant", "caption")),
		body,
	).Build(), nil
}

// headingFor names the screen after the thing it is about, when the caller said
// what that was.
func headingFor(title string) string {
	if title == "" {
		return "Sources"
	}
	return "Sources for " + title
}

// summaryFor is the sentence under the heading, and it is where the count
// finally says something actionable.
//
// One candidate is worth stating explicitly. "Nothing changed when I picked a
// different source" and "there was only ever one source" are the same picture
// from the outside, and the whole reason selection reported a count at all was
// that they were indistinguishable.
func summaryFor(res app.PlaybackSourcesResult) string {
	switch {
	case res.Total == 0:
		return "Nothing here can be played yet."
	case res.Total == 1:
		return "One release, which is the only thing this item has."
	default:
		return strconv.Itoa(res.Total) + " releases, best for this device first."
	}
}

// noSources is the honest empty state (ADR 0116).
//
// It is not an error and must not read as one. An item with no candidate is a
// perfectly ordinary thing — a metadata-only import, or a source that has
// stopped offering it — and presenting that as a failure was what sent people
// looking for a bug in playback.
func noSources() ui.El {
	return ui.Stack("vertical", 2,
		ui.Text(ui.Prop("text", "No playable release"), ui.Prop("variant", "label")),
		ui.Text(ui.Prop("text", "This item is in the library, and nothing is currently offering "+
			"a file for it. That happens when it was added for its metadata alone, or when the "+
			"source that had it has stopped. Installing or configuring a stream provider in "+
			"Extensions is what changes it."), ui.Prop("variant", "caption")),
		ui.Button("Extensions", "secondary", ui.OnTap(ui.Navigate(screenExtensions, nil))))
}

// sourceRows renders each candidate as a row the picker can draw and press.
//
// Pressing one is an ordinary play of that Part — there is no "switch source"
// action and there does not need to be, because choosing a release *is* naming
// which Part to play. That is why this screen needed no new action kind.
func sourceRows(sources []app.PlaybackSource, nodeID, title string, params map[string]any) []any {
	rows := make([]any, 0, len(sources))
	for _, src := range sources {
		label := src.Release
		if label == "" {
			label = "Unnamed release"
		}
		if src.Chosen {
			// Said rather than implied by position. A list ordered by a ranking
			// nobody can see is indistinguishable from an arbitrary one.
			label += " — currently chosen"
		}
		quality := src.Quality
		if src.Why != "" {
			if quality != "" {
				quality += " · "
			}
			quality += src.Why
		}
		rows = append(rows, map[string]any{
			"label":    label,
			"provider": src.Provider,
			"quality":  quality,
			"action": ui.Invoke(playPartAction, map[string]any{
				paramPartID: string(src.PartID),
				paramNodeID: nodeID,
				"title":     title,
				"poster":    stringParam(params, "poster"),
			}),
		})
	}
	return rows
}
