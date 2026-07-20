// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package screens is the Platform's SDUI emit-side (ADR 0029). It builds UINode
// trees from the application query services using the mosaic-sdui Go producer
// binding, and serves them through a name-keyed registry. It is a projection
// surface, exactly like the GraphQL resolvers: every builder calls application
// query services and nothing else — no store, no transaction.
//
// Screens are Platform-emitted. A module contributes content through its
// provider roles (ADR 0027); the Platform owns how that content is shown. A
// screen's Action names a Platform GraphQL operation, so the emitted tree and
// the data its actions drive share one transport.
package screens

import (
	"context"
	"strconv"
	"strings"

	sdui "github.com/mosaic-media/mosaic-sdui/sdui"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// contentQueries is the slice of the application query surface the screen
// builders read. Narrowing to an interface keeps the emit-side a projection of
// the services (like a GraphQL resolver) and makes the builders testable without
// standing up a full Service. *app.Service satisfies it.
type contentQueries interface {
	SearchAvailableContent(context.Context, app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error)
}

// Service renders named screens. It holds the query surface the builders read
// from; it opens nothing of its own.
type Service struct {
	content contentQueries
}

// NewService wires the emit-side to the application services.
func NewService(a *app.Service) *Service {
	return &Service{content: a}
}

// Render builds the named screen for the caller, with the given params. An
// unknown name is NotFound. The returned Node is the root the client renders.
func (s *Service) Render(ctx context.Context, name string, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	switch name {
	case "search":
		return s.searchScreen(ctx, caller, params)
	default:
		return sdui.Node{}, contracts.NewError(contracts.NotFound, "no screen named "+name)
	}
}

// searchScreen is the user's discovery surface: a search bar over the results
// grid. With no query it shows an empty prompt; with one it runs
// SearchAvailableContent and renders each result as a card (ADR 0028's union —
// in-library and virtual candidates in one list, told apart by their badge and
// action).
func (s *Service) searchScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	text := strings.TrimSpace(stringParam(params, "text"))
	bar := sdui.Component(sdui.TypeSearchBar,
		sdui.Prop("placeholder", "Search movies and shows"),
		sdui.Prop("value", text),
		// The client substitutes the field value for $value when it submits,
		// re-rendering this screen with the new query.
		sdui.Act(sdui.Navigate("search", map[string]any{"text": "$value"})),
	)

	if text == "" {
		return sdui.Screen(
			sdui.Prop("title", "Search"),
			sdui.Child(bar, sdui.EmptyState("search", "Search for something to add")),
		), nil
	}

	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{Caller: caller, Text: text})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Results) == 0 {
		return sdui.Screen(
			sdui.Prop("title", "Search"),
			sdui.Child(bar, sdui.EmptyState("search", "No results for \""+text+"\"")),
		), nil
	}

	cards := make([]sdui.Node, 0, len(res.Results))
	for _, r := range res.Results {
		cards = append(cards, resultCard(r))
	}
	return sdui.Screen(
		sdui.Prop("title", "Search"),
		sdui.Child(bar, sdui.Grid(sdui.Child(cards...))),
	), nil
}

// resultCard renders one search result. An in-library result carries a badge and
// opens its detail; a virtual one carries a materialise action that invokes
// importContent with its ref (ADR 0028 — import is the crossing into the
// library).
func resultCard(r v1.SearchResult) sdui.Node {
	opts := []sdui.Option{}
	if year := yearLabel(r.Year); year != "" {
		opts = append(opts, sdui.Subtitle(year))
	}
	if r.Poster != "" {
		opts = append(opts, sdui.Poster(r.Poster))
	}
	if r.InLibrary {
		opts = append(opts,
			sdui.BadgeText("In library"),
			sdui.Act(sdui.Navigate("detail", map[string]any{"nodeId": string(r.NodeID)})),
		)
	} else {
		opts = append(opts, sdui.Act(sdui.Invoke("importContent", map[string]any{"ref": refInput(r.Ref)})))
	}
	return sdui.PosterCard(r.Title, string(r.Ref.MediaType), opts...)
}

// refInput serializes a ContentRef into the shape the importContent mutation's
// ContentRefInput accepts, so a card's materialise action round-trips the ref.
func refInput(ref v1.ContentRef) map[string]any {
	return map[string]any{
		"provider":       ref.Provider,
		"nativeId":       ref.NativeID,
		"nativeType":     ref.NativeType,
		"mediaType":      string(ref.MediaType),
		"externalScheme": ref.ExternalScheme,
		"externalId":     ref.ExternalID,
	}
}

// yearLabel renders a release year, empty when unknown.
func yearLabel(year int) string {
	if year <= 0 {
		return ""
	}
	return strconv.Itoa(year)
}

// stringParam reads a string screen param, tolerating an absent or non-string
// value.
func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	s, _ := params[key].(string)
	return s
}
