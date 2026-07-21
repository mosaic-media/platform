// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"

	sdui "github.com/mosaic-media/sdui/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// detailScreen renders a rich content detail — a backdrop+logo hero, poster,
// cast, genres and (for a series) a season selector over an episode list with
// per-episode synopses (ADR 0034). It is ref-based and serves both planes: a
// virtual item and an in-library one render from the same metadata, differing
// only in the primary action. A nodeId-only navigation (no ref) falls back to
// the structural library view, since metadata is fetched by ref.
func (s *Service) detailScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	if refMap, ok := params[paramRef].(map[string]any); ok {
		return s.richDetail(ctx, caller, refFromParam(refMap), params)
	}
	nodeID := stringParam(params, paramNodeID)
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
			sdui.Invoke(importContentMutation, map[string]any{paramRef: refInput(ref)})))
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

	return screen(title, body...), nil
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
	if sv := stringParam(params, paramSeason); sv != "" {
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
			"action": sdui.Navigate(screenDetail, map[string]any{paramRef: refInput(ref), paramSeason: strconv.Itoa(n)}),
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
				sdui.Act(sdui.Navigate(screenDetail, map[string]any{paramNodeID: string(c.ID)})),
			))
		}
		body = append(body, sdui.Section("Contents", sdui.Child(sdui.Grid(sdui.Child(cards...)))))
	}
	return screen(n.Title, body...), nil
}
