// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	sdui "github.com/mosaic-media/sdui/sdui"
	"github.com/mosaic-media/sdui/ui"

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
		return nil, contracts.NewError(contracts.InvalidArgument, "detail screen needs a nodeId or ref param")
	}
	return s.libraryDetail(ctx, caller, nodeID)
}

// richDetail builds the full detail for a ref (ADR 0034). It reads the ref's
// metadata (and library status) through PreviewContent, which resolves both
// planes, then composes: a Hero (backdrop, clearlogo, meta pills, overview and
// the primary action), the poster docked in the hero's aside, a cast rail, a
// genre row, and for a series a SeasonSelector over the selected season's
// episodes. Every image is routed through the artwork proxy (ADR 0030).
func (s *Service) richDetail(ctx context.Context, caller v1.Caller, ref v1.ContentRef, params map[string]any) (sdui.Node, error) {
	res, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: ref})
	if err != nil {
		return nil, err
	}
	m := res.Metadata
	title := m.Title
	if title == "" {
		title = ref.NativeID
	}

	// Meta pills: rating · year · then a runtime (film) or a "N Seasons · M
	// Episodes" count (series).
	var pills []string
	if m.Rating > 0 {
		pills = append(pills, fmt.Sprintf("★ %.1f", m.Rating))
	}
	if y := yearLabel(m.Year); y != "" {
		pills = append(pills, y)
	}
	if sc := seasonEpisodeLabel(m.Episodes); sc != "" {
		pills = append(pills, sc)
	} else if m.Runtime != "" {
		pills = append(pills, m.Runtime)
	}

	// Primary action. A virtual item can only be added; an in-library one can be
	// played, when something in its tree actually has bytes. Play is offered on
	// the presence of a Part rather than on being in the library, so the button
	// never appears with nothing behind it — the dead-end affordance ADR 0036
	// exists to prevent.
	var actions ui.El
	switch {
	case !res.InLibrary:
		actions = ui.Actions(ui.Button("Add to library", "primary",
			ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(ref)}))))
	default:
		els := []ui.El{}
		if part, ok := s.content.FirstPlayablePart(ctx, caller, res.NodeID); ok {
			els = append(els, ui.Button("Play", "primary", ui.OnTap(ui.Invoke(playPartAction, map[string]any{
				paramPartID: string(part.ID),
				"title":     title,
				"poster":    s.art(m.Poster),
			}))))
		}
		// Re-importing an in-library item refreshes its candidate releases
		// (additive — nothing is removed). It is offered explicitly rather than
		// run on every view because an aggregator fan-out costs seconds and most
		// views never lead to a play.
		els = append(els, ui.Button("Refresh sources", "secondary",
			ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(ref)}))))
		els = append(els, ui.Badge("In library", ui.ToneSuccess))
		actions = ui.Actions(els...)
	}

	// The paneled detail hero: a full-bleed backdrop (the light source) with the
	// title/meta/genres/overview/action in a floating GLASS panel, and a glass
	// info panel docked beside it (the aside) — so the acrylic material has large
	// surfaces to light. Fills the Screen's full-bleed slot.
	heroEls := []ui.El{
		ui.Title(title),
		ui.Backdrop(s.art(m.Backdrop)),
		ui.When(ref.MediaType != "", ui.Prop("kicker",
			strings.ToUpper(strings.ReplaceAll(string(ref.MediaType), "_", " ")))),
		ui.When(len(pills) > 0, ui.Meta(pills...)),
		ui.When(m.Logo != "", ui.Logo(s.art(m.Logo))),
		ui.When(m.Overview != "", ui.Overview(m.Overview)),
		actions,
		ui.Aside(s.detailInfoPanel(m, ref)),
	}
	if len(m.Genres) > 0 {
		tags := make([]ui.El, 0, len(m.Genres))
		for _, g := range m.Genres {
			tags = append(tags, ui.GenreTag(g))
		}
		heroEls = append(heroEls, ui.Prop("showTags", true), ui.Slot("tags", tags...))
	}

	body := []ui.El{ui.Slot("bleed", ui.Component("DetailHero", heroEls...))}

	if len(m.Cast) > 0 {
		chips := make([]ui.El, 0, len(m.Cast))
		for _, p := range m.Cast {
			chips = append(chips, ui.PersonChip(p.Name,
				ui.When(p.Role != "", ui.Prop("role", p.Role)),
				// Through the artwork proxy like every other remote image
				// (ADR 0030): a headshot on a third-party CDN would otherwise
				// leak the viewer's IP and depend on that CDN's CORS.
				ui.When(p.Photo != "", ui.Prop("avatar", s.art(p.Photo)))))
		}
		body = append(body, ui.Section("Cast", ui.Carousel(chips...)))
	}

	if len(m.Episodes) > 0 {
		body = append(body, s.episodesSection(ref, m.Episodes, params))
	}

	return ui.Screen(ui.Group(body...)).Build(), nil
}

// detailInfoPanel builds the glass info aside that docks beside the hero panel:
// a large rating, then label/value rows drawn from the metadata Mosaic actually
// has (type, year, episodes/runtime, genres). It renders as an acrylic panel.
func (s *Service) detailInfoPanel(m v1.ContentMetadata, ref v1.ContentRef) ui.El {
	els := []ui.El{}
	if m.Rating > 0 {
		els = append(els, ui.Prop("rating", fmt.Sprintf("%.1f", m.Rating)), ui.Prop("ratingLabel", "Rating"))
	}
	rows := make([]map[string]any, 0, 4)
	row := func(label, value string) {
		if value != "" {
			rows = append(rows, map[string]any{"label": label, "value": value})
		}
	}
	if mt := string(ref.MediaType); mt != "" {
		row("Type", titleWords(mt))
	}
	row("Year", yearLabel(m.Year))
	if len(m.Episodes) > 0 {
		row("Episodes", fmt.Sprintf("%d", len(m.Episodes)))
	} else {
		row("Runtime", m.Runtime)
	}
	if len(m.Genres) > 0 {
		row("Genres", strings.Join(m.Genres, ", "))
	}
	els = append(els, ui.Prop("rows", rows))
	return ui.Component("InfoPanel", els...)
}

// titleWords title-cases an underscored/spaced token ("tv_series" → "Tv Series")
// for display, replacing the deprecated strings.Title for this small use.
func titleWords(s string) string {
	words := strings.Fields(strings.ReplaceAll(s, "_", " "))
	for i, w := range words {
		if w != "" {
			words[i] = strings.ToUpper(w[:1]) + w[1:]
		}
	}
	return strings.Join(words, " ")
}

// seasonEpisodeLabel renders a series' "2 Seasons · 19 Episodes" summary from
// its episode preview, counting distinct seasons and total episodes. It is empty
// for a film (no episodes), letting the caller fall back to a runtime pill.
func seasonEpisodeLabel(episodes []v1.EpisodePreview) string {
	if len(episodes) == 0 {
		return ""
	}
	seasons := make(map[int]struct{}, 4)
	for _, e := range episodes {
		seasons[e.Season] = struct{}{}
	}
	seasonWord, episodeWord := "Seasons", "Episodes"
	if len(seasons) == 1 {
		seasonWord = "Season"
	}
	if len(episodes) == 1 {
		episodeWord = "Episode"
	}
	return fmt.Sprintf("%d %s · %d %s", len(seasons), seasonWord, len(episodes), episodeWord)
}

// episodesSection builds a series' episode browser: a SeasonSelector across the
// seasons (each switching by re-navigating with a season param) over the
// selected season's episodes as EpisodeRows carrying the synopsis and still
// (ADR 0034). The selected season comes from the season param, defaulting to the
// first.
func (s *Service) episodesSection(ref v1.ContentRef, episodes []v1.EpisodePreview, params map[string]any) *ui.Element {
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
			"action": ui.Navigate(screenDetail, map[string]any{paramRef: refInput(ref), paramSeason: strconv.Itoa(n)}),
		})
	}
	selector := ui.Component("SeasonSelector",
		ui.Prop("seasons", seasonEntries), ui.Prop("selected", strconv.Itoa(selected)))

	rows := make([]ui.El, 0, len(bySeason[selected]))
	for _, e := range bySeason[selected] {
		rows = append(rows, ui.EpisodeRow(e.Title,
			ui.Prop("index", strconv.Itoa(e.Episode)),
			ui.When(e.Overview != "", ui.Overview(e.Overview)),
			ui.When(e.Thumbnail != "", ui.Prop("thumbnail", s.art(e.Thumbnail))),
		))
	}
	return ui.Section("Episodes", selector, ui.Stack("vertical", 3, rows...))
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
		return nil, err
	}
	n := res.Node

	body := []ui.El{ui.DetailHeader(n.Title, ui.Meta(string(n.MediaType), string(n.Kind)))}
	if len(res.Children) > 0 {
		cards := make([]ui.El, 0, len(res.Children))
		for _, c := range res.Children {
			cards = append(cards, ui.PosterCard(c.Title, string(c.MediaType),
				ui.OnTap(ui.Navigate(screenDetail, map[string]any{paramNodeID: string(c.ID)}))))
		}
		body = append(body, ui.Section("Contents", ui.Grid(cards...)))
	}
	return ui.Screen(ui.Title(n.Title), ui.Group(body...)).Build(), nil
}
