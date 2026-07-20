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
	ListModuleCatalogs(context.Context, app.ListModuleCatalogsQuery) (app.ListModuleCatalogsResult, error)
	ListCatalogItems(context.Context, app.ListCatalogItemsQuery) (app.ListCatalogItemsResult, error)
	GetContentNode(context.Context, v1.GetContentNodeQuery) (v1.GetContentNodeResult, error)
	PreviewContent(context.Context, app.PreviewContentQuery) (app.PreviewContentResult, error)
}

// Service renders named screens. It holds the query surface the builders read
// from, and an artwork rewriter that routes remote poster/backdrop URLs through
// the Platform's artwork proxy (ADR 0030); it opens nothing of its own.
type Service struct {
	content contentQueries
	artwork func(string) string
}

// NewService wires the emit-side to the application services. artwork rewrites a
// remote image URL to a Platform-proxied one; a nil rewriter passes URLs
// through unchanged (a Service built without the proxy).
func NewService(a *app.Service, artwork func(string) string) *Service {
	if artwork == nil {
		artwork = func(u string) string { return u }
	}
	return &Service{content: a, artwork: artwork}
}

// Render builds the named screen for the caller, with the given params. An
// unknown name is NotFound. The returned Node is the root the client renders.
func (s *Service) Render(ctx context.Context, name string, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	switch name {
	case "search":
		return s.searchScreen(ctx, caller, params)
	case "collections":
		return s.collectionsScreen(ctx, caller)
	case "catalog":
		return s.catalogScreen(ctx, caller, params)
	case "detail":
		return s.detailScreen(ctx, caller, params)
	default:
		return sdui.Node{}, contracts.NewError(contracts.NotFound, "no screen named "+name)
	}
}

// detailScreen renders a content item — either a materialised library node
// (params.nodeId) or a virtual item not yet in the library (params.ref). Both a
// virtual and an in-library card open this; the difference is what it shows.
func (s *Service) detailScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	if refMap, ok := params["ref"].(map[string]any); ok {
		return s.virtualDetail(ctx, caller, refFromParam(refMap))
	}
	nodeID := stringParam(params, "nodeId")
	if nodeID == "" {
		return sdui.Node{}, contracts.NewError(contracts.InvalidArgument, "detail screen needs a nodeId or ref param")
	}
	return s.libraryDetail(ctx, caller, nodeID)
}

// virtualDetail previews a not-yet-materialised item: its descriptive metadata,
// and the single library affordance a virtual item has — Add to library, which
// materialises it (ADR 0028). A ref that turns out to be in the library already
// falls through to its real detail rather than showing a duplicate preview.
func (s *Service) virtualDetail(ctx context.Context, caller v1.Caller, ref v1.ContentRef) (sdui.Node, error) {
	res, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: ref})
	if err != nil {
		return sdui.Node{}, err
	}
	if res.InLibrary && res.NodeID != "" {
		return s.libraryDetail(ctx, caller, string(res.NodeID))
	}

	m := res.Metadata
	title := m.Title
	if title == "" {
		title = ref.NativeID
	}
	opts := []sdui.Option{
		sdui.Slot("actions", sdui.Button("Add to library", "primary",
			sdui.Invoke("importContent", map[string]any{"ref": refInput(ref)}))),
	}
	if m.Overview != "" {
		opts = append(opts, sdui.Overview(m.Overview))
	}
	if len(m.Genres) > 0 {
		opts = append(opts, sdui.Genres(m.Genres...))
	}
	if y := yearLabel(m.Year); y != "" {
		opts = append(opts, sdui.Meta(y, string(ref.MediaType)))
	}
	if m.Poster != "" {
		opts = append(opts, sdui.Poster(s.artwork(m.Poster)))
	}
	if m.Backdrop != "" {
		opts = append(opts, sdui.Backdrop(s.artwork(m.Backdrop)))
	}
	return sdui.Screen(sdui.Prop("title", title), sdui.Child(sdui.DetailHeader(title, opts...))), nil
}

// libraryDetail renders a materialised node: its header, and its direct children
// as cards that open their own detail (one level per screen, since the tree is
// of variable depth — ADR 0013). A film's child is its feature item; a series'
// children are its seasons.
func (s *Service) libraryDetail(ctx context.Context, caller v1.Caller, nodeID string) (sdui.Node, error) {
	res, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{
		Caller: caller, NodeID: v1.NodeID(nodeID), WithChildren: true,
	})
	if err != nil {
		return sdui.Node{}, err
	}
	n := res.Node

	body := []sdui.Node{sdui.DetailHeader(n.Title, sdui.Meta(string(n.MediaType), string(n.Kind)))}
	if len(res.Children) > 0 {
		cards := make([]sdui.Node, 0, len(res.Children))
		for _, c := range res.Children {
			cards = append(cards, sdui.PosterCard(c.Title, string(c.MediaType),
				sdui.Act(sdui.Navigate("detail", map[string]any{"nodeId": string(c.ID)})),
			))
		}
		body = append(body, sdui.Section("Contents", sdui.Child(sdui.Grid(sdui.Child(cards...)))))
	}
	return sdui.Screen(sdui.Prop("title", n.Title), sdui.Child(body...)), nil
}

// refFromParam reads a ContentRef out of a screen's ref param (a decoded JSON
// object).
func refFromParam(m map[string]any) v1.ContentRef {
	get := func(k string) string { s, _ := m[k].(string); return s }
	return v1.ContentRef{
		Provider: get("provider"), NativeID: get("nativeId"), NativeType: get("nativeType"),
		MediaType: v1.MediaType(get("mediaType")), ExternalScheme: get("externalScheme"), ExternalID: get("externalId"),
	}
}

// collectionsScreen is the admin's entry to curation: the collections the
// enabled modules expose, each a row that opens the catalog's items. Browsing is
// a read — nothing is published until an item's materialise action runs (ADR
// 0028).
func (s *Service) collectionsScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	res, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Catalogs) == 0 {
		return sdui.Screen(
			sdui.Prop("title", "Collections"),
			sdui.Child(sdui.EmptyState("collections", "No collections yet — configure a module addon first")),
		), nil
	}
	rows := make([]sdui.Node, 0, len(res.Catalogs))
	for _, c := range res.Catalogs {
		rows = append(rows, sdui.Button(c.Catalog.Name, "secondary", sdui.Navigate("catalog", map[string]any{
			"moduleId": c.ModuleID, "catalogId": c.Catalog.ID, "nativeType": c.Catalog.NativeType,
		})))
	}
	return sdui.Screen(
		sdui.Prop("title", "Collections"),
		sdui.Child(sdui.Stack("vertical", 8, sdui.Child(rows...))),
	), nil
}

// catalogScreen lists one collection's items as cards an admin can publish. Like
// the search grid, virtual items carry a materialise action and in-library ones
// a badge and a detail navigation.
func (s *Service) catalogScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, "moduleId")
	catalogID := stringParam(params, "catalogId")
	if moduleID == "" || catalogID == "" {
		return sdui.Node{}, contracts.NewError(contracts.InvalidArgument, "catalog screen needs moduleId and catalogId params")
	}
	res, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
		Caller: caller, ModuleID: moduleID, CatalogID: catalogID, NativeType: stringParam(params, "nativeType"),
	})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Items) == 0 {
		return sdui.Screen(
			sdui.Prop("title", "Collection"),
			sdui.Child(sdui.EmptyState("collections", "This collection is empty")),
		), nil
	}
	cards := make([]sdui.Node, 0, len(res.Items))
	for _, it := range res.Items {
		cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary, it.NodeID))
	}
	return sdui.Screen(
		sdui.Prop("title", "Collection"),
		sdui.Child(sdui.Grid(sdui.Child(cards...))),
	), nil
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
		cards = append(cards, s.resultCard(r))
	}
	return sdui.Screen(
		sdui.Prop("title", "Search"),
		sdui.Child(bar, sdui.Grid(sdui.Child(cards...))),
	), nil
}

// resultCard renders one search result as a content card.
func (s *Service) resultCard(r v1.SearchResult) sdui.Node {
	return s.contentCard(r.Ref, r.Title, r.Year, r.Poster, r.InLibrary, r.NodeID)
}

// contentCard renders a content item — a search result or a catalog entry,
// which carry the same fields. Both open a detail screen on click: an in-library
// item to its node's detail, a virtual one to a preview whose sole library
// affordance is Add to library (ADR 0028 — materialising is the deliberate act,
// made on the detail rather than the card). An in-library item also carries a
// badge so the two read apart at a glance. The poster is routed through the
// artwork proxy (ADR 0030).
func (s *Service) contentCard(ref v1.ContentRef, title string, year int, poster string, inLibrary bool, nodeID v1.NodeID) sdui.Node {
	opts := []sdui.Option{}
	if y := yearLabel(year); y != "" {
		opts = append(opts, sdui.Subtitle(y))
	}
	if poster != "" {
		opts = append(opts, sdui.Poster(s.artwork(poster)))
	}
	if inLibrary {
		opts = append(opts,
			sdui.BadgeText("In library"),
			sdui.Act(sdui.Navigate("detail", map[string]any{"nodeId": string(nodeID)})),
		)
	} else {
		opts = append(opts, sdui.Act(sdui.Navigate("detail", map[string]any{"ref": refInput(ref)})))
	}
	return sdui.PosterCard(title, string(ref.MediaType), opts...)
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
