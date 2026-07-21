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
	"encoding/json"
	"fmt"
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
	ModuleSettingsUI(context.Context, app.ModuleSettingsUIQuery) (app.ModuleSettingsUIResult, error)
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

// art proxies a non-empty image URL through the artwork rewriter (ADR 0030),
// passing an empty URL and a Service built without a rewriter through unchanged.
func (s *Service) art(u string) string {
	if u == "" || s.artwork == nil {
		return u
	}
	return s.artwork(u)
}

// Render builds the named screen for the caller, with the given params. An
// unknown name is NotFound. The returned Node is the root the client renders.
func (s *Service) Render(ctx context.Context, name string, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	switch name {
	case "shell":
		return s.shellScreen()
	case "home":
		return s.homeScreen(ctx, caller)
	case "search":
		return s.searchScreen(ctx, caller, params)
	case "collections":
		return s.collectionsScreen(ctx, caller)
	case "catalog":
		return s.catalogScreen(ctx, caller, params)
	case "detail":
		return s.detailScreen(ctx, caller, params)
	case "settings":
		return s.settingsScreen(ctx, caller, params)
	default:
		return sdui.Node{}, contracts.NewError(contracts.NotFound, "no screen named "+name)
	}
}

// settingsScreen hosts a module's own contributed settings UI (ADR 0038). The
// Platform owns the frame; the module fills it — the settings screen renders the
// UINode tree the module returned through ModuleSettingsUI, validated by the app
// service. It takes a moduleId param, defaulting to the Stremio module (the only
// one that provides a settings UI today); a settings index over several modules
// is a later addition.
func (s *Service) settingsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, "moduleId")
	if moduleID == "" {
		moduleID = "stremio"
	}
	res, err := s.content.ModuleSettingsUI(ctx, app.ModuleSettingsUIQuery{Caller: caller, ModuleID: moduleID})
	if err != nil {
		return sdui.Node{}, err
	}
	var node sdui.Node
	if err := json.Unmarshal(res.UI, &node); err != nil {
		return sdui.Node{}, contracts.WrapError(contracts.Internal, "decode module settings UI", err)
	}
	return node, nil
}

// detailScreen renders a rich content detail — a backdrop+logo hero, poster,
// cast, genres and (for a series) a season selector over an episode list with
// per-episode synopses (ADR 0034). It is ref-based and serves both planes: a
// virtual item and an in-library one render from the same metadata, differing
// only in the primary action. A nodeId-only navigation (no ref) falls back to
// the structural library view, since metadata is fetched by ref.
func (s *Service) detailScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	if refMap, ok := params["ref"].(map[string]any); ok {
		return s.richDetail(ctx, caller, refFromParam(refMap), params)
	}
	nodeID := stringParam(params, "nodeId")
	if nodeID == "" {
		return sdui.Node{}, contracts.NewError(contracts.InvalidArgument, "detail screen needs a nodeId or ref param")
	}
	return s.libraryDetail(ctx, caller, nodeID)
}

// richDetail builds the full detail for a ref (ADR 0034). It reads the ref's
// metadata (and library status) through PreviewContent, which resolves both
// planes, then composes: a HeroBanner (backdrop, clearlogo, meta pills, overview
// and the primary action), the poster docked in the hero's aside, a cast rail, a
// genre row, and for a series a SeasonSelector over the selected season's
// episodes. Every image is routed through the artwork proxy (ADR 0030).
func (s *Service) richDetail(ctx context.Context, caller v1.Caller, ref v1.ContentRef, params map[string]any) (sdui.Node, error) {
	res, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: ref})
	if err != nil {
		return sdui.Node{}, err
	}
	m := res.Metadata
	title := m.Title
	if title == "" {
		title = ref.NativeID
	}

	// Hero meta pills: year · media type · runtime · rating.
	var pills []string
	if y := yearLabel(m.Year); y != "" {
		pills = append(pills, y)
	}
	if mt := string(ref.MediaType); mt != "" {
		pills = append(pills, mt)
	}
	if m.Runtime != "" {
		pills = append(pills, m.Runtime)
	}
	if m.Rating > 0 {
		pills = append(pills, fmt.Sprintf("★ %.1f", m.Rating))
	}

	// Primary action: Add to library (virtual) or an in-library marker.
	var actions sdui.Option
	if res.InLibrary {
		actions = sdui.Slot("actions", sdui.Badge("In library", sdui.ToneSuccess))
	} else {
		actions = sdui.Slot("actions", sdui.Button("Add to library", "primary",
			sdui.Invoke("importContent", map[string]any{"ref": refInput(ref)})))
	}

	heroOpts := []sdui.Option{sdui.Backdrop(s.art(m.Backdrop)), sdui.Meta(pills...), actions}
	if m.Logo != "" {
		heroOpts = append(heroOpts, sdui.Logo(s.art(m.Logo)))
	}
	if m.Overview != "" {
		heroOpts = append(heroOpts, sdui.Overview(m.Overview))
	}
	if m.Poster != "" {
		heroOpts = append(heroOpts, sdui.Slot("aside", posterBox(s.art(m.Poster))))
	}

	body := []sdui.Node{sdui.HeroBanner(title, heroOpts...)}

	if len(m.Genres) > 0 {
		tags := make([]sdui.Node, 0, len(m.Genres))
		for _, g := range m.Genres {
			tags = append(tags, sdui.GenreTag(g))
		}
		body = append(body, sdui.Section("Genres", sdui.Child(sdui.Stack("horizontal", 2, sdui.Child(tags...)))))
	}

	if len(m.Cast) > 0 {
		chips := make([]sdui.Node, 0, len(m.Cast))
		for _, p := range m.Cast {
			opts := []sdui.Option{}
			if p.Role != "" {
				opts = append(opts, sdui.Prop("role", p.Role))
			}
			chips = append(chips, sdui.PersonChip(p.Name, opts...))
		}
		body = append(body, sdui.Section("Cast", sdui.Child(sdui.Carousel(sdui.Child(chips...)))))
	}

	if len(m.Episodes) > 0 {
		body = append(body, s.episodesSection(ref, m.Episodes, params))
	}

	return sdui.Screen(sdui.Prop("title", title), sdui.Child(body...)), nil
}

// episodesSection builds a series' episode browser: a SeasonSelector across the
// seasons (each switching by re-navigating with a season param) over the
// selected season's episodes as EpisodeRows carrying the synopsis and still
// (ADR 0034). The selected season comes from the season param, defaulting to the
// first.
func (s *Service) episodesSection(ref v1.ContentRef, episodes []v1.EpisodePreview, params map[string]any) sdui.Node {
	order := make([]int, 0)
	bySeason := make(map[int][]v1.EpisodePreview)
	for _, e := range episodes {
		if _, seen := bySeason[e.Season]; !seen {
			order = append(order, e.Season)
		}
		bySeason[e.Season] = append(bySeason[e.Season], e)
	}
	// Default to the first real season, skipping a season 0 of specials when a
	// numbered season exists; the season param overrides.
	selected := order[0]
	for _, n := range order {
		if n >= 1 {
			selected = n
			break
		}
	}
	if sv := stringParam(params, "season"); sv != "" {
		if n, err := strconv.Atoi(sv); err == nil {
			if _, ok := bySeason[n]; ok {
				selected = n
			}
		}
	}

	seasonEntries := make([]map[string]any, 0, len(order))
	for _, n := range order {
		seasonEntries = append(seasonEntries, map[string]any{
			"id":     strconv.Itoa(n),
			"label":  fmt.Sprintf("Season %d", n),
			"action": sdui.Navigate("detail", map[string]any{"ref": refInput(ref), "season": strconv.Itoa(n)}),
		})
	}
	selector := sdui.Component(sdui.TypeSeasonSelector,
		sdui.Prop("seasons", seasonEntries), sdui.Prop("selected", strconv.Itoa(selected)))

	rows := make([]sdui.Node, 0, len(bySeason[selected]))
	for _, e := range bySeason[selected] {
		opts := []sdui.Option{sdui.Prop("index", strconv.Itoa(e.Episode))}
		if e.Overview != "" {
			opts = append(opts, sdui.Overview(e.Overview))
		}
		if e.Thumbnail != "" {
			opts = append(opts, sdui.Prop("thumbnail", s.art(e.Thumbnail)))
		}
		rows = append(rows, sdui.EpisodeRow(e.Title, opts...))
	}
	return sdui.Section("Episodes",
		sdui.Child(selector, sdui.Stack("vertical", 3, sdui.Child(rows...))))
}

// posterBox docks a poster image (its natural 2:3) into a hero's aside slot.
func posterBox(url string) sdui.Node {
	return sdui.Component("Box",
		sdui.Prop("style", map[string]any{
			"width": 168, "aspectRatio": "2 / 3", "radius": "md",
			"overflow": "hidden", "bg": "surface-raised", "shadow": "2",
		}),
		sdui.Child(sdui.Component("Image",
			sdui.Prop("src", url), sdui.Prop("placeholder", " "),
			sdui.Prop("style", map[string]any{"width": "full", "height": "full"}))),
	)
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

// homeScreen is the default landing surface: a hero over rows of the enabled
// modules' catalogs (Cinemeta's Popular Movies/Series, etc. — ADR 0028's virtual
// plane, browsed not materialised). Each row is a carousel of cards that open a
// detail; the hero is the first catalog's first item, enriched with its backdrop
// and logo. Browsing is a read, so nothing here writes.
func (s *Service) homeScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	cats, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(cats.Catalogs) == 0 {
		return sdui.Screen(
			sdui.Prop("title", "Home"),
			sdui.Child(sdui.EmptyState("collections", "Nothing here yet — add an addon in Settings to browse content")),
		), nil
	}

	const (
		maxRows     = 6
		maxRowItems = 20
	)
	body := make([]sdui.Node, 0, maxRows+1)
	heroAdded := false
	rows := 0
	for _, c := range cats.Catalogs {
		if rows >= maxRows {
			break
		}
		items, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
			Caller: caller, ModuleID: c.ModuleID, CatalogID: c.Catalog.ID, NativeType: c.Catalog.NativeType,
		})
		if err != nil || len(items.Items) == 0 {
			continue
		}
		if !heroAdded {
			if hero, ok := s.heroFromItem(ctx, caller, items.Items[0]); ok {
				body = append(body, hero)
				heroAdded = true
			}
		}
		cards := make([]sdui.Node, 0, maxRowItems)
		for i, it := range items.Items {
			if i >= maxRowItems {
				break
			}
			cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary, it.NodeID))
		}
		body = append(body, sdui.Section(c.Catalog.Name, sdui.Child(sdui.Carousel(sdui.Child(cards...)))))
		rows++
	}
	if len(body) == 0 {
		return sdui.Screen(
			sdui.Prop("title", "Home"),
			sdui.Child(sdui.EmptyState("collections", "Nothing to show yet — try adding an addon in Settings")),
		), nil
	}
	return sdui.Screen(sdui.Prop("title", "Home"), sdui.Child(body...)), nil
}

// heroFromItem builds a home hero from a catalog item, enriching it with the
// backdrop, logo and overview its lightweight card lacks (ADR 0034). A metadata
// fetch that fails just yields no hero rather than failing the home screen.
func (s *Service) heroFromItem(ctx context.Context, caller v1.Caller, it v1.CatalogItem) (sdui.Node, bool) {
	prev, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: it.Ref})
	if err != nil {
		return sdui.Node{}, false
	}
	m := prev.Metadata
	title := m.Title
	if title == "" {
		title = it.Title
	}
	opts := []sdui.Option{
		sdui.Backdrop(s.art(m.Backdrop)),
		sdui.Slot("actions", sdui.Button("View", "primary",
			sdui.Navigate("detail", map[string]any{"ref": refInput(it.Ref)}))),
	}
	if m.Logo != "" {
		opts = append(opts, sdui.Logo(s.art(m.Logo)))
	}
	if m.Overview != "" {
		opts = append(opts, sdui.Overview(m.Overview))
	}
	var pills []string
	if y := yearLabel(m.Year); y != "" {
		pills = append(pills, y)
	}
	if m.Rating > 0 {
		pills = append(pills, fmt.Sprintf("★ %.1f", m.Rating))
	}
	if len(pills) > 0 {
		opts = append(opts, sdui.Meta(pills...))
	}
	return sdui.HeroBanner(title, opts...), true
}

// shellScreen is the server-emitted application frame (ADR 0031): the nav rail
// and top bar. The Shell renders this and fills its content region with the
// current screen; it owns no chrome of its own. It is static for now — a live
// session (ADR 0032) will push shell changes over the socket.
func (s *Service) shellScreen() (sdui.Node, error) {
	return sdui.Component("AppShell",
		sdui.Prop("title", "Mosaic"),
		sdui.Slot("nav",
			navItem("Home", "home", "home"),
			navItem("Collections", "list", "collections"),
			navItem("Settings", "settings", "settings"),
		),
		// The search bar lives in the top bar and is always present, so there is no
		// Search nav item. Typing takes over the content region (a live `input`);
		// clearing it returns to the current screen.
		sdui.Slot("topbar",
			sdui.Component(sdui.TypeSearchBar,
				sdui.Prop("placeholder", "Search for anime, movies, shows…"),
			),
		),
	), nil
}

// navItem builds one sidebar navigation button that navigates to a screen.
func navItem(label, icon, screen string) sdui.Node {
	return sdui.Component("NavItem",
		sdui.Prop("label", label), sdui.Prop("icon", icon), sdui.Prop("screen", screen),
		sdui.Act(sdui.Navigate(screen, nil)),
	)
}

// searchScreen is the results surface that takes over the content region while a
// user types in the always-present top-bar search (ADR 0032). It carries no
// search bar of its own — the top bar holds the query. It runs
// SearchAvailableContent and renders each result as a card (ADR 0028's union of
// in-library and virtual candidates). An empty query does not reach here: the
// live session re-renders the current screen instead of an empty search.
func (s *Service) searchScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	text := strings.TrimSpace(stringParam(params, "text"))
	if text == "" {
		return sdui.Screen(
			sdui.Prop("title", "Search"),
			sdui.Child(sdui.EmptyState("search", "Type to search for something to add")),
		), nil
	}

	res, err := s.content.SearchAvailableContent(ctx, app.SearchAvailableContentQuery{Caller: caller, Text: text})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Results) == 0 {
		return sdui.Screen(
			sdui.Prop("title", "Search"),
			sdui.Child(sdui.EmptyState("search", "No results for \""+text+"\"")),
		), nil
	}

	cards := make([]sdui.Node, 0, len(res.Results))
	for _, r := range res.Results {
		cards = append(cards, s.resultCard(r))
	}
	return sdui.Screen(
		sdui.Prop("title", "Search for \""+text+"\""),
		sdui.Child(sdui.Grid(sdui.Child(cards...))),
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
		opts = append(opts, sdui.Poster(s.art(poster)))
	}
	// Both planes open the same ref-based rich detail (ADR 0034); PreviewContent
	// resolves in-library from the ref, so the detail shows the right action. An
	// in-library card also carries a badge so the two read apart on the grid.
	if inLibrary {
		opts = append(opts, sdui.BadgeText("In library"))
	}
	opts = append(opts, sdui.Act(sdui.Navigate("detail", map[string]any{"ref": refInput(ref)})))
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
