// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
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
	// Genres ride the meta line as one pill, as the mockups draw them, rather
	// than as a second row of tags beneath it. They carry no action here — a
	// GenreTag with nothing to navigate to was decoration occupying a whole row.
	if len(m.Genres) > 0 {
		pills = append(pills, strings.Join(m.Genres, " · "))
	}
	// The age rating as the source labels it for its region. Display-only text
	// (the scales are national and not comparable), and an empty one must not be
	// read as "suitable for everyone" — so it is omitted rather than defaulted.
	if m.Certification != "" {
		pills = append(pills, m.Certification)
	}

	// Primary action. A virtual item can only be added; an in-library one can be
	// played, when something in its tree actually has bytes. Play is offered on
	// the presence of a Part rather than on being in the library, so the button
	// never appears with nothing behind it — the dead-end affordance ADR 0036
	// exists to prevent.
	// The trailer is watchable in either plane and belongs to neither: it is not
	// a Part, so it never goes through playPart — it opens on the site that
	// hosts it. Offered only when a URL can actually be built, because a Trailer
	// carries a site and a key rather than a link and a site nobody can address
	// is an affordance with nothing behind it (ADR 0036).
	trailer, hasTrailer := trailerAction(m.Trailers)

	var actions ui.El
	switch {
	case !res.InLibrary:
		els := []ui.El{ui.Button("Add to library", "primary",
			ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(ref)})))}
		if hasTrailer {
			els = append(els, ui.Button("Trailer", "secondary", ui.IconName("play"), ui.OnTap(trailer)))
		}
		actions = ui.Actions(els...)
	default:
		els := []ui.El{}
		part, playable, err := s.content.FirstPlayablePart(ctx, caller, res.NodeID)
		if err != nil {
			return nil, err
		}
		if playable {
			// Where this viewer got to, if anywhere (ADR 0046). The state is
			// keyed on the *item* that has the bytes rather than on the work
			// above it, because that is what a viewer resumes — an episode, not
			// a series.
			state, stateErr := s.content.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{
				Caller: caller, NodeID: part.NodeID,
			})
			// Not fatal — a detail screen without a resume offset is still a
			// detail screen — but not silent either. Swallowing this outright
			// makes "Resume never appears" indistinguishable from "nothing has
			// been watched", which is a difference only a log can carry.
			if stateErr != nil {
				telemetry.From(ctx).For("screens").Warn("reading playback state failed; offering Play instead of Resume",
					telemetry.Identifier("node", string(part.NodeID)),
					telemetry.Err(stateErr))
			}
			resumable := state.Found && state.State.ResumeAt() > 0

			playInput := map[string]any{
				paramPartID: string(part.ID),
				"nodeId":    string(part.NodeID),
				"title":     title,
				"poster":    s.art(m.Poster),
			}

			label := "Play"
			if resumable {
				// Naming the time is the difference between an affordance a
				// viewer trusts and one they test. "Resume" alone leaves them
				// wondering whether it remembers the right place.
				label = "Resume " + positionLabel(state.State.ResumeAt())
			}
			els = append(els, ui.Button(label, "primary", ui.OnTap(ui.Invoke(playPartAction, playInput))))

			if resumable {
				// Start over is offered rather than assumed, and it does not
				// clear the position: someone who starts again and stops after
				// five minutes should not have lost the hour they had before
				// they will inevitably change their mind.
				restart := map[string]any{}
				for k, v := range playInput {
					restart[k] = v
				}
				restart["restart"] = true
				els = append(els, ui.Button("Start over", "secondary",
					ui.OnTap(ui.Invoke(playPartAction, restart))))
			}
		}
		// Re-importing an in-library item refreshes its candidate releases
		// (additive — nothing is removed). It is offered explicitly rather than
		// run on every view because an aggregator fan-out costs seconds and most
		// views never lead to a play.
		if hasTrailer {
			els = append(els, ui.Button("Trailer", "secondary", ui.IconName("play"), ui.OnTap(trailer)))
		}
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
		// The poster docks ahead of the copy, as the mockups draw it. The screen
		// has had one in hand all along — it was already routing it through the
		// proxy for the play action's payload — and no way to render it.
		ui.When(m.Poster != "", ui.Poster(s.art(m.Poster))),
		actions,
		ui.Aside(s.detailInfoPanel(m, ref)),
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
		// Watched marks come from the materialised tree, which only an in-library
		// series has; a virtual one passes no node and shows no marks.
		var seriesNode v1.NodeID
		if res.InLibrary {
			seriesNode = res.NodeID
		}
		body = append(body, s.episodesSection(ctx, caller, ref, seriesNode, m.Episodes, params))
	}

	// The franchise this work belongs to, and then what a viewer of it tends to
	// want next. Both were being fetched and thrown away: TMDB fills Similar and
	// Collection on every detail read, and this screen rendered neither, so the
	// work of resolving them was paid for and discarded on every view.
	//
	// The franchise list includes the work being described — the SDK says so
	// plainly — so it is filtered on the ref already held rather than trusting
	// the source to have excluded it.
	if m.Collection != nil {
		if rail := s.relatedRail(m.Collection.Name, m.Collection.Items, ref); rail != nil {
			body = append(body, rail)
		}
	}
	if rail := s.relatedRail("More like this", m.Similar, ref); rail != nil {
		body = append(body, rail)
	}

	return ui.Screen(ui.Group(body...)).Build(), nil
}

// detailInfoPanel builds the glass info aside that docks beside the hero panel:
// a large rating, then label/value rows drawn from the metadata Mosaic actually
// has (type, year, episodes/runtime, genres). It renders as an acrylic panel.
func (s *Service) detailInfoPanel(m v1.ContentMetadata, ref v1.ContentRef) ui.El {
	els := []ui.El{ui.Heading("About this title")}
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
func (s *Service) episodesSection(ctx context.Context, caller v1.Caller, ref v1.ContentRef, seriesNode v1.NodeID, episodes []v1.EpisodePreview, params map[string]any) *ui.Element {
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

	// A finished mark per episode, for an in-library series (ADR 0046). Read once
	// for the whole visible season rather than per row, and only when there is a
	// materialised tree to read from.
	var watched map[int]bool
	if seriesNode != "" {
		watched = s.watchedInSeason(ctx, caller, seriesNode, selected)
	}

	rows := make([]ui.El, 0, len(bySeason[selected]))
	for _, e := range bySeason[selected] {
		rows = append(rows, ui.EpisodeRow(e.Title,
			ui.Prop("index", strconv.Itoa(e.Episode)),
			ui.When(e.Overview != "", ui.Overview(e.Overview)),
			ui.When(e.Thumbnail != "", ui.Prop("thumbnail", s.art(e.Thumbnail))),
			ui.When(watched[e.Episode], ui.Prop("watched", true)),
		))
	}
	return ui.Section("Episodes", selector, ui.Stack("vertical", 3, rows...))
}

// watchedInSeason returns the finished episodes of one season of an in-library
// series, keyed by episode number, for the watched checks on its rows.
//
// The episode list on screen is the provider's live preview (ADR 0034), which
// carries season and episode numbers but no node ids; playback state is keyed by
// node (ADR 0046). This bridges the two through the materialised tree: a series'
// children are its seasons and a season's children its episodes, each carrying
// its number as NaturalOrder, so the tree maps (season, episode) back to the
// node the position is stored under. It reads only the selected season — one
// season walk and one batched state read — because that is all the rows show.
//
// Every failure returns no marks rather than an error: an unmarked episode row
// is still a row, and a detail screen that cannot read progress should lose its
// ticks, not its episodes.
func (s *Service) watchedInSeason(ctx context.Context, caller v1.Caller, seriesNode v1.NodeID, season int) map[int]bool {
	seasons, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{
		Caller: caller, NodeID: seriesNode, WithChildren: true,
	})
	if err != nil {
		telemetry.From(ctx).For("screens").Warn("reading season tree for watched marks failed",
			telemetry.Identifier("series", string(seriesNode)), telemetry.Err(err))
		return nil
	}
	var seasonNode v1.NodeID
	for _, c := range seasons.Children {
		if c.Kind == v1.NodeContainer && int(c.NaturalOrder) == season {
			seasonNode = c.ID
			break
		}
	}
	if seasonNode == "" {
		return nil
	}

	eps, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{
		Caller: caller, NodeID: seasonNode, WithChildren: true,
	})
	if err != nil {
		telemetry.From(ctx).For("screens").Warn("reading episode nodes for watched marks failed",
			telemetry.Identifier("season", string(seasonNode)), telemetry.Err(err))
		return nil
	}
	byNumber := make(map[int]v1.NodeID, len(eps.Children))
	ids := make([]v1.NodeID, 0, len(eps.Children))
	for _, ep := range eps.Children {
		if ep.ItemType != v1.ItemEpisode {
			continue
		}
		byNumber[int(ep.NaturalOrder)] = ep.ID
		ids = append(ids, ep.ID)
	}
	if len(ids) == 0 {
		return nil
	}

	states, err := s.content.ListPlaybackStates(ctx, v1.ListPlaybackStatesQuery{Caller: caller, NodeIDs: ids})
	if err != nil {
		telemetry.From(ctx).For("screens").Warn("reading playback states for watched marks failed",
			telemetry.Err(err))
		return nil
	}
	watched := make(map[int]bool, len(byNumber))
	for num, id := range byNumber {
		if st, ok := states.States[id]; ok && st.Finished {
			watched[num] = true
		}
	}
	return watched
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

// positionLabel renders a resume offset the way a viewer reads a clock.
//
// Hours are omitted below an hour rather than shown as 0:, because "0:47:12"
// reads as a duration and "47:12" reads as a place in a film.
func positionLabel(d time.Duration) string {
	total := int(d.Round(time.Second).Seconds())
	h, m, sec := total/3600, (total%3600)/60, total%60
	if h > 0 {
		return fmt.Sprintf("%d:%02d:%02d", h, m, sec)
	}
	return fmt.Sprintf("%d:%02d", m, sec)
}

// trailerAction opens a title's trailer on the site that hosts it, preferring an
// official video over a fan upload and the first of equals otherwise.
//
// A Trailer carries a site and a key rather than a URL, so the link has to be
// built — and only for sites whose URL shape is known. An unrecognised site
// yields no action at all rather than a guessed address: a button that opens a
// 404 is worse than one that is not drawn.
func trailerAction(trailers []v1.Trailer) (ui.Action, bool) {
	pick := -1
	for i, t := range trailers {
		if trailerURL(t) == "" {
			continue
		}
		if pick == -1 || (t.Official && !trailers[pick].Official) {
			pick = i
		}
	}
	if pick == -1 {
		return ui.Action{}, false
	}
	return ui.OpenURL(trailerURL(trailers[pick])), true
}

// trailerURL is a trailer's watch page, empty when its site is one this does not
// know how to address.
func trailerURL(t v1.Trailer) string {
	if t.Key == "" {
		return ""
	}
	switch strings.ToLower(t.Site) {
	case "youtube":
		return "https://www.youtube.com/watch?v=" + url.QueryEscape(t.Key)
	case "vimeo":
		return "https://vimeo.com/" + url.PathEscape(t.Key)
	default:
		return ""
	}
}

// relatedRail renders a list of related titles as a carousel of poster cards,
// dropping the work being described and returning nil when nothing is left.
//
// Each card opens the ref-based detail, exactly as a catalog or search card
// does: a related item is a virtual item (ADR 0028), and opening one is a read.
func (s *Service) relatedRail(title string, items []v1.RelatedItem, self v1.ContentRef) ui.El {
	cards := make([]ui.El, 0, len(items))
	for _, it := range items {
		if it.Ref == self {
			continue
		}
		cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
	}
	if len(cards) == 0 {
		return nil
	}
	return ui.Section(title, ui.Carousel(cards...))
}
