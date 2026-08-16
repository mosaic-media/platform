// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"net/url"
	"sort"
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

// detailScreen renders a rich content detail — a cinematic hero over an episode
// browser, the cast, the technical facts of the release behind the play button,
// and what to watch next (sdk#3). It is ref-based and serves both planes: a
// virtual item and an in-library one render from the same metadata, differing in
// the primary action and in how much they can say about bytes they may not have.
// A nodeId navigation renders the same tree from the object graph instead
// (platform#62): the stored document, the node's own artwork and the materialised
// tree, with no provider call at all.
func (s *Service) detailScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	if refMap, ok := params[paramRef].(map[string]any); ok {
		return s.richDetail(ctx, caller, refFromParam(refMap), params)
	}
	nodeID := stringParam(params, paramNodeID)
	if nodeID == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "detail screen needs a nodeId or ref param")
	}
	return s.libraryDetail(ctx, caller, nodeID, params)
}

// richDetail builds the full detail for a ref, in the order the design states
// it: hero, episodes, cast, technical facts, related.
//
// Episodes come before cast deliberately. Someone opening a series they are
// part-way through is looking for the next episode, and putting a rail of
// headshots between them and it makes the common case scroll past the rare one.
func (s *Service) richDetail(ctx context.Context, caller v1.Caller, ref v1.ContentRef, params map[string]any) (sdui.Node, error) {
	res, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: ref})
	if err != nil {
		return nil, err
	}
	return s.renderDetail(ctx, caller, ref, res, nil, params)
}

// renderDetail is the detail tree itself, over metadata that has already been
// resolved.
//
// It is split from the fetch because the two planes now resolve differently and
// must still render identically (platform#62): a virtual item asks its provider
// live, and a library item reads the stored document and its own tree. A
// second renderer for the second plane is how the library detail would quietly
// become the poorer one.
func (s *Service) renderDetail(ctx context.Context, caller v1.Caller, ref v1.ContentRef, res app.PreviewContentResult, knownSeasons []int, params map[string]any) (sdui.Node, error) {
	m := res.Metadata
	title := m.Title
	if title == "" {
		title = ref.NativeID
	}

	// The selected season, its episodes and this viewer's progress through them
	// — computed once, because the hero's resume label, the panel's "up next"
	// and the episode rows are three readings of the same answer. It comes first
	// because it decides which release the hero is about to play.
	season := s.seasonView(ctx, caller, res, m.Episodes, knownSeasons, params)

	// The release behind the play button, and everything known about it. It is
	// resolved once here and read by three surfaces — the meta pills, the
	// playback panel and the facts grid — because it is one query and they all
	// describe the same bytes.
	var playing *v1.Part
	var facts releaseFacts
	if res.InLibrary {
		part, playable, partErr := s.playTarget(ctx, caller, res, season)
		if partErr != nil {
			return nil, partErr
		}
		if playable {
			playing = &part
			facts = factsFor(part)
		}
	}

	heroEls := []ui.El{
		ui.Title(title),
		ui.Backdrop(s.art(m.Backdrop)),
		ui.When(m.Logo != "", ui.Logo(s.art(m.Logo))),
		ui.When(m.Poster != "", ui.Poster(s.art(m.Poster))),
		ui.When(m.Overview != "", ui.Overview(m.Overview)),
	}
	if k := kickerLabel(ref, season.order); k != "" {
		heroEls = append(heroEls, ui.Kicker(k))
	}
	if pills := metaPills(m, facts); len(pills) > 0 {
		heroEls = append(heroEls, ui.Meta(pills...))
	}
	if c := creditsLine(m.Crew); c != "" {
		heroEls = append(heroEls, ui.Credits(c))
	}
	heroEls = append(heroEls,
		s.heroActions(ctx, caller, res, ref, m, title, playing, season),
		ui.Aside(playbackPanel(m, ref, facts, playing, season)),
	)

	body := []ui.El{ui.Slot("bleed", ui.Component("DetailHero", heroEls...))}

	if len(season.all) > 0 {
		body = append(body, s.episodesSection(ref, m, season))
	}

	if len(m.Cast) > 0 {
		chips := make([]ui.El, 0, len(m.Cast))
		for _, p := range m.Cast {
			chips = append(chips, ui.PersonChip(p.Name,
				ui.When(p.Role != "", ui.Role(p.Role)),
				// Through the artwork proxy like every other remote image
				// (platform#20): a headshot on a third-party CDN would otherwise
				// leak the viewer's IP and depend on that CDN's CORS.
				ui.When(p.Photo != "", ui.Avatar(s.art(p.Photo)))))
		}
		// The rail's track is the chip's own width. Left at the browse default
		// of 196 the 132px portraits sit in 196px columns, which reads as an
		// erratic gap rather than as a rail.
		body = append(body, ui.Section("Cast", ui.Carousel(ui.ItemWidth(132), ui.Group(chips...))))
	}

	if grid := s.factsGrid(ctx, caller, ref, facts); grid != nil {
		body = append(body, grid)
	}

	// The franchise this work belongs to, then what a viewer of it tends to want
	// next. The design draws one related rail; the franchise rail is kept beside
	// it because it is a different question with real data behind it, and
	// dropping it would delete a working capability for a layout reason.
	//
	// The franchise list includes the work being described — the SDK says so
	// plainly — so it is filtered on the ref already held rather than trusting
	// the source to have excluded it.
	if m.Collection != nil {
		if rail := s.relatedRail(ctx, caller, m.Collection.Name, m.Collection.Items, ref); rail != nil {
			body = append(body, rail)
		}
	}
	if rail := s.relatedRail(ctx, caller, "More like this", m.Similar, ref); rail != nil {
		body = append(body, rail)
	}

	return ui.Screen(ui.Group(body...)).Build(), nil
}

// kickerLabel is the eyebrow above the title — "Series · 2 seasons", "Film".
//
// The season count lives here rather than in the meta pills because it is what
// the thing is, not a fact about it: a pill row reads as a list of attributes
// and "2 Seasons · 19 Episodes" sitting among a rating and a certificate reads
// as one more attribute rather than as the shape of the work.
// It takes the resolved season order rather than an episode list. Counting
// distinct seasons among the episodes on hand was correct while a detail always
// held every episode of a series, and became wrong the moment a library detail
// began reading one season at a time (platform#62): a seventy-five-season programme
// announced itself as "Series · 1 season".
func kickerLabel(ref v1.ContentRef, seasons []int) string {
	kind := mediaTypeWord(string(ref.MediaType))
	if kind == "" {
		return ""
	}
	if n := len(seasons); n > 0 {
		return fmt.Sprintf("%s · %d %s", kind, n, plural(n, "season"))
	}
	return kind
}

// mediaTypeWord names a media type the way the design writes it — "Series",
// "Film" — rather than the source's own token.
func mediaTypeWord(mediaType string) string {
	switch strings.ToLower(strings.ReplaceAll(mediaType, "_", " ")) {
	case "":
		return ""
	case "movie", "film":
		return "Film"
	case "tv series", "series", "tv", "show":
		return "Series"
	default:
		return titleWords(mediaType)
	}
}

// metaPills is the row under the title: what it scored, when it is from, what it
// is about, who it is for, and — when Mosaic actually holds the bytes — what
// they are.
//
// The rating carries no source name. v1.ContentMetadata.Rating is a bare
// float with no scale or attribution on it, so "8.7 IMDb" as the design writes
// it cannot be stated truthfully; the number is shown and the claim about where
// it came from is not.
func metaPills(m v1.ContentMetadata, facts releaseFacts) []string {
	var pills []string
	if m.Rating > 0 {
		pills = append(pills, fmt.Sprintf("★ %.1f", m.Rating))
	}
	if y := yearLabel(m.Year); y != "" {
		pills = append(pills, y)
	}
	// Genres ride the meta line as one pill, as the design draws them, rather
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
	// Only a film has one runtime; a series' episodes each have their own and
	// they go on the rows.
	if len(m.Episodes) == 0 && m.Runtime != "" {
		pills = append(pills, m.Runtime)
	}
	if q := facts.qualityPill(); q != "" {
		pills = append(pills, q)
	}
	if a := facts.audioPill(); a != "" {
		pills = append(pills, a)
	}
	return pills
}

// creditsLine is the crew line under the actions — "Created by X · Directed by
// Y". Empty when the source carries no crew, which is most addons.
//
// Names are grouped by job so a show with two creators reads as "Created by A
// and B" rather than as the same phrase twice.
func creditsLine(crew []v1.Person) string {
	if len(crew) == 0 {
		return ""
	}
	order := make([]string, 0, 2)
	byJob := make(map[string][]string, 2)
	for _, p := range crew {
		if p.Name == "" || p.Role == "" {
			continue
		}
		if _, seen := byJob[p.Role]; !seen {
			order = append(order, p.Role)
		}
		byJob[p.Role] = append(byJob[p.Role], p.Name)
	}
	phrases := make([]string, 0, len(order))
	for _, job := range order {
		phrases = append(phrases, jobPhrase(job)+" "+joinNames(byJob[job]))
	}
	return strings.Join(phrases, " · ")
}

// jobPhrase turns a crew job into the way a credit is worded.
func jobPhrase(job string) string {
	switch strings.ToLower(job) {
	case "creator":
		return "Created by"
	case "director":
		return "Directed by"
	case "writer", "screenplay":
		return "Written by"
	default:
		return job + ":"
	}
}

// joinNames lists names the way a person would say them.
func joinNames(names []string) string {
	switch len(names) {
	case 0:
		return ""
	case 1:
		return names[0]
	case 2:
		return names[0] + " and " + names[1]
	default:
		return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
	}
}

// heroActions builds the hero's control row: one primary play/add, then the
// secondary affordances as pills.
//
// The design draws a download control beside them. There is none here: Mosaic
// has no offline capability at all — no role in the SDK declares one and nothing
// stores bytes for later — so the control is absent rather than drawn inert,
// which is the dead-end affordance platform#24 exists to prevent.
func (s *Service) heroActions(ctx context.Context, caller v1.Caller, res app.PreviewContentResult,
	ref v1.ContentRef, m v1.ContentMetadata, title string, playing *v1.Part, season seasonView) ui.El {

	// The trailer is watchable in either plane and belongs to neither: it is not
	// a Part, so it never goes through playPart — it opens on the site that
	// hosts it. Offered only when a URL can actually be built, because a Trailer
	// carries a site and a key rather than a link, and a site nobody can address
	// is an affordance with nothing behind it (platform#24).
	trailer, hasTrailer := trailerAction(m.Trailers)

	els := make([]ui.El, 0, 5)

	// Curating the library is an administrator's authority, not a viewer's
	// (platform#44), so the control that does it is drawn only for a caller who
	// holds it (platform#24). It was drawn for everybody while everybody was the
	// same account; the first ordinary account pressed it and got nothing —
	// which is the dead end the paragraph above this function is about, on the
	// same screen.
	canImport := s.content.CallerCan(ctx, caller, app.ActionContentImport, "content")

	if !res.InLibrary {
		if canImport {
			// Play comes first and Add second, which is the ordering platform#73
			// argues for: a viewer wants to watch the thing, and adding it is
			// what the Platform has to do to let them. Both are drawn only for a
			// caller who may import, because pressing Play here adds it —
			// the same authority, honestly gated (platform#44).
			els = append(els, ui.Button("Play", "primary", ui.IconName("play"),
				ui.OnTap(ui.Invoke(playPartAction, map[string]any{
					paramRef: refInput(ref),
					"title":  title,
					"poster": s.art(m.Poster),
				}))))
			els = append(els, ui.Button("Add to library", "secondary", ui.IconName("plus"),
				ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(ref)}))))
		}
		if hasTrailer {
			els = append(els, ui.Button("Trailer", "secondary", ui.IconName("play"), ui.OnTap(trailer)))
		}
		return ui.Actions(els...)
	}

	if playing != nil {
		// Where this viewer got to, if anywhere (platform#26). The state is keyed
		// on the item that has the bytes rather than on the work above it,
		// because that is what a viewer resumes — an episode, not a series.
		state, stateErr := s.content.GetPlaybackState(ctx, v1.GetPlaybackStateQuery{
			Caller: caller, NodeID: playing.NodeID,
		})
		// Not fatal — a detail screen without a resume offset is still a detail
		// screen — but not silent either. Swallowing this outright makes "Resume
		// never appears" indistinguishable from "nothing has been watched",
		// which is a difference only a log can carry.
		if stateErr != nil {
			telemetry.From(ctx).For("screens").Warn("reading playback state failed; offering Play instead of Resume",
				telemetry.Identifier("node", string(playing.NodeID)),
				telemetry.Err(stateErr))
		}
		resumable := state.Found && state.State.ResumeAt() > 0

		playInput := map[string]any{
			paramPartID: string(playing.ID),
			"nodeId":    string(playing.NodeID),
			"title":     title,
			"poster":    s.art(m.Poster),
		}

		// Naming what is being played rather than the clock reading, when the
		// thing has a name. "Resume S2 E7" tells a viewer of a series the one
		// thing they wanted to know; "Resume 47:12" makes them open the episode
		// list to find out which one it is. On a first play the name matters
		// more, not less: it is the screen saying which episode this button
		// picked, rather than picking one quietly.
		episode := season.episodeOf(playing.NodeID)
		label := "Play"
		switch {
		case resumable && episode != "":
			label = "Resume " + episode
		case resumable:
			label = "Resume " + positionLabel(state.State.ResumeAt())
		case episode != "":
			label = "Play " + episode
		}
		els = append(els, ui.Button(label, "primary", ui.IconName("play"),
			ui.OnTap(ui.Invoke(playPartAction, playInput))))

		// The way to the candidate set (platform#71). Offered beside Play rather
		// than instead of it: the ranking is right often enough that making
		// everyone choose would be a worse default than choosing for them, and
		// wrong often enough that there has to be a way through.
		els = append(els, ui.Button("Sources", "secondary", ui.OnTap(ui.Navigate(screenSources, map[string]any{
			paramNodeID: string(playing.NodeID),
			"title":     title,
			"poster":    s.art(m.Poster),
		}))))

		if resumable {
			// Start over is offered rather than assumed, and it does not clear
			// the position: someone who starts again and stops after five
			// minutes should not have lost the hour they had before they will
			// inevitably change their mind.
			restart := map[string]any{}
			for k, v := range playInput {
				restart[k] = v
			}
			restart["restart"] = true
			els = append(els, ui.Button("Start over", "secondary", ui.OnTap(ui.Invoke(playPartAction, restart))))
		}
	}

	if hasTrailer {
		els = append(els, ui.Button("Trailer", "secondary", ui.IconName("play"), ui.OnTap(trailer)))
	}

	// The square pills. Watched is a real toggle over playback state (platform#26)
	// — the dispatch case has existed since the state store landed and no screen
	// had ever emitted it, so the only way to mark something watched was to
	// watch it.
	if playing != nil {
		els = append(els, ui.IconButton("check", "Mark watched", "pill",
			ui.OnTap(ui.Invoke(setWatchedAction, map[string]any{
				"nodeId": string(playing.NodeID), "finished": true,
			}))))
	}
	// Re-importing an in-library item refreshes its candidate releases
	// (additive — nothing is removed). It is offered explicitly rather than run
	// on every view because an aggregator fan-out costs seconds and most views
	// never lead to a play.
	if canImport {
		els = append(els, ui.IconButton("refresh", "Refresh sources", "pill",
			ui.OnTap(ui.Invoke(importContentMutation, map[string]any{paramRef: refInput(ref)}))))
	}

	return ui.Actions(els...)
}

// playbackPanel is the glass panel docked beside the hero: what a viewer is
// about to get, and what comes after it.
//
// The design's panel opens with "Playing on — Living Room TV" and closes with a
// "Change device" control. Neither is here: a Session carries a DeviceID with no
// name on it, there is no device registry to name one from, and no capability
// role in the SDK describes a playback target. Rows Mosaic cannot answer are
// dropped rather than filled with the local machine.
func playbackPanel(m v1.ContentMetadata, ref v1.ContentRef, facts releaseFacts, playing *v1.Part, season seasonView) ui.El {
	if playing != nil {
		rows := make([]any, 0, 4)
		row := func(label, value string) {
			if value != "" {
				rows = append(rows, map[string]any{"label": label, "value": value})
			}
		}
		row("Quality", facts.qualityPill())
		row("Audio", facts.audioPill())
		row("Subtitles", facts.subtitlesLabel())
		if next := season.nextUp(); next != nil {
			row("Up next", episodeCode(*next)+" · "+next.Title)
		}
		if len(rows) > 0 {
			return ui.Component("InfoPanel", ui.Heading("This playback"), ui.Rows(rows))
		}
	}

	// Nothing is playable, so the panel describes the title instead of a
	// release. This is the virtual-item case and the common one on first view.
	els := []ui.El{ui.Heading("About this title")}
	if m.Rating > 0 {
		els = append(els, ui.Rating(fmt.Sprintf("%.1f", m.Rating)), ui.RatingLabel("Rating"))
	}
	rows := make([]any, 0, 4)
	row := func(label, value string) {
		if value != "" {
			rows = append(rows, map[string]any{"label": label, "value": value})
		}
	}
	if mt := mediaTypeWord(string(ref.MediaType)); mt != "" {
		row("Type", mt)
	}
	row("Year", yearLabel(m.Year))
	// Seasons rather than episodes, and read from the season order rather than
	// from the episodes on hand.
	//
	// A library detail reads one season at a time (platform#62), so counting
	// m.Episodes here said "Episodes 6" over a selector offering seventy-five
	// seasons — a number that was true of the read and false of the series. The
	// season count is the one this panel can state honestly without reading a
	// tree it deliberately does not read.
	if n := len(season.order); n > 0 {
		row("Seasons", strconv.Itoa(n))
	} else {
		row("Runtime", m.Runtime)
	}
	if len(m.Genres) > 0 {
		row("Genres", strings.Join(m.Genres, ", "))
	}
	els = append(els, ui.Rows(rows))
	return ui.Component("InfoPanel", els...)
}

// factsGrid is the four-up row of technical cards under the cast.
//
// It is dropped entirely when nothing can be said — a virtual item has no bytes
// to describe, and four empty cards are worse than none.
func (s *Service) factsGrid(ctx context.Context, caller v1.Caller, ref v1.ContentRef, facts releaseFacts) ui.El {
	cards := make([]ui.El, 0, 4)
	add := func(c ui.El) {
		if c != nil {
			cards = append(cards, c)
		}
	}
	if facts.part.ID != "" {
		add(facts.videoCard())
		add(facts.audioCard())
		add(facts.deliveryCard(clientCodecs(ctx)))
	}
	add(metadataCard(ref, s.providerName(ctx, caller, ref.Provider)))
	if len(cards) == 0 {
		return nil
	}
	// Four columns at the design's 1320 content width, reflowing below it. The
	// gap is the design's 16 rather than the browse grids' 24: these are one
	// band of related facts, not a wall of independent cards.
	grid := append([]ui.El{ui.MinColumnWidth(300), ui.Gap(4)}, cards...)
	return ui.Section("", ui.Grid(grid...))
}

// providerName is a module's own name for itself, for attributing metadata to
// something a person recognises rather than to a module id. Falls back to the id
// when the module contributes no settings surface to be named by.
func (s *Service) providerName(ctx context.Context, caller v1.Caller, moduleID string) string {
	if moduleID == "" {
		return ""
	}
	res, err := s.content.ListSettingsModules(ctx, app.ListSettingsModulesQuery{Caller: caller})
	if err != nil {
		return moduleID
	}
	for _, mod := range res.Modules {
		if mod.ModuleID == moduleID && mod.Name != "" {
			return mod.Name
		}
	}
	return moduleID
}

// playTarget is the release the hero's primary action plays, and which episode
// it belongs to.
//
// FirstPlayablePart deliberately does not walk past a work's direct children,
// so it reports nothing playable for every series: a series' children are its
// seasons, and picking an episode inside one is a choice the application layer
// declined to make silently. That limit stands, and this is the screen making
// the choice out loud instead — it resolves a named episode and the button
// says which one, so nothing is defaulted behind the viewer's back.
//
// The order is the order a viewer would expect: the episode they are part-way
// through, then the first they have not finished, then the season's first. A
// film has no season view and falls through to the work, which is unchanged.
func (s *Service) playTarget(ctx context.Context, caller v1.Caller,
	res app.PreviewContentResult, season seasonView) (v1.Part, bool, error) {

	if node, ok := season.playTargetNode(); ok {
		// The episode itself is the item, so its releases are read directly
		// rather than through FirstPlayablePart, which answers about a work by
		// looking at its children and finds none under a leaf.
		parts, err := s.content.ListNodeParts(ctx, app.ListNodePartsQuery{Caller: caller, NodeID: node})
		if err != nil {
			return v1.Part{}, false, err
		}
		if len(parts.Parts) > 0 {
			return parts.Parts[0], true, nil
		}
	}
	return s.content.FirstPlayablePart(ctx, caller, res.NodeID)
}

// playTargetNode is the episode node the hero would play: the one part-way
// through, then the first unfinished, then the season's first.
func (s seasonView) playTargetNode() (v1.NodeID, bool) {
	if len(s.nodes) == 0 {
		return "", false
	}
	byNumber := make(map[int]v1.NodeID, len(s.nodes))
	for id, n := range s.nodes {
		byNumber[n] = id
	}
	// Part-way through beats not-started: a half-watched episode is the one
	// thing on this screen a viewer is unambiguously in the middle of.
	for _, e := range s.episodes {
		st, seen := s.states[e.Episode]
		if seen && !st.Finished && st.Position > 0 {
			if id, ok := byNumber[e.Episode]; ok {
				return id, true
			}
		}
	}
	for _, e := range s.episodes {
		if st, seen := s.states[e.Episode]; !seen || !st.Finished {
			if id, ok := byNumber[e.Episode]; ok {
				return id, true
			}
		}
	}
	if id, ok := byNumber[s.episodes[0].Episode]; ok && len(s.episodes) > 0 {
		return id, true
	}
	return "", false
}

// seasonView is one season of a series as the screen needs it: which season is
// shown, its episodes, and this viewer's progress through them.
//
// It exists because three surfaces asked the same question and would otherwise
// have asked it three times — the hero's resume label needs to know which
// episode a Part is, the panel's "up next" needs the first unwatched one, and
// the rows need a progress bar each.
type seasonView struct {
	// all is every episode of the series, ungrouped, as the provider gave them.
	all []v1.EpisodePreview
	// order is the season numbers in the order they first appear.
	order []int
	// selected is the season on screen.
	selected int
	// episodes is the selected season's episodes, in order.
	episodes []v1.EpisodePreview
	// states is this viewer's playback state per episode number of the selected
	// season, empty for a virtual series (no nodes, so nothing is keyed).
	states map[int]v1.PlaybackState
	// quality is the release quality per episode number, where the episode has a
	// playable Part.
	quality map[int]string
	// nodes maps an episode's node id back to its number, so a Part can say
	// which episode it belongs to.
	nodes map[v1.NodeID]int
}

// episodeOf names the episode a node is, "S2 E7", or empty when the node is not
// one of this season's episodes.
func (s seasonView) episodeOf(node v1.NodeID) string {
	n, ok := s.nodes[node]
	if !ok {
		return ""
	}
	return fmt.Sprintf("S%d E%d", s.selected, n)
}

// nextUp is the first episode of the shown season that has been neither finished
// nor started, which is what a viewer part-way through a season is about to
// want. Nil when the season is finished or nothing has been started in it —
// "up next" on a series nobody has begun is just "episode one", which the list
// below already says.
func (s seasonView) nextUp() *v1.EpisodePreview {
	if len(s.states) == 0 {
		return nil
	}
	for i, e := range s.episodes {
		st, seen := s.states[e.Episode]
		if !seen {
			return &s.episodes[i]
		}
		if !st.Finished && st.Position == 0 {
			return &s.episodes[i]
		}
	}
	return nil
}

// seasonView resolves the season to show and reads this viewer's progress
// through it. An error anywhere costs the progress, never the episodes.
// knownSeasons, when given, is the complete season list read from the tree's
// season containers — the library plane's way of listing seventy-five seasons
// having read the episodes of only one. The virtual plane passes nil and the
// order is derived from the provider's preview, which carries every season
// because a provider answers with the whole series at once.
func (s *Service) seasonView(ctx context.Context, caller v1.Caller, res app.PreviewContentResult,
	episodes []v1.EpisodePreview, knownSeasons []int, params map[string]any) seasonView {

	view := seasonView{all: episodes}
	if len(episodes) == 0 && len(knownSeasons) == 0 {
		return view
	}

	bySeason := make(map[int][]v1.EpisodePreview)
	for _, e := range episodes {
		if _, seen := bySeason[e.Season]; !seen {
			view.order = append(view.order, e.Season)
		}
		bySeason[e.Season] = append(bySeason[e.Season], e)
	}
	if len(knownSeasons) > 0 {
		view.order = knownSeasons
	}
	if len(view.order) == 0 {
		return view
	}
	// Default to the first real season, skipping a season 0 of specials when a
	// numbered season exists; the season param overrides.
	view.selected = view.order[0]
	for _, n := range view.order {
		if n >= 1 {
			view.selected = n
			break
		}
	}
	if sv := stringParam(params, paramSeason); sv != "" {
		if n, err := strconv.Atoi(sv); err == nil {
			if _, ok := bySeason[n]; ok {
				view.selected = n
			}
		}
	}
	view.episodes = bySeason[view.selected]

	// Progress comes from the materialised tree, which only an in-library series
	// has; a virtual one shows its episodes with no marks on them.
	if !res.InLibrary || res.NodeID == "" {
		return view
	}
	s.fillSeasonProgress(ctx, caller, res.NodeID, &view)
	return view
}

// fillSeasonProgress bridges the provider's episode preview to the materialised
// tree and reads this viewer's state over it.
//
// The episode list on screen is the provider's live preview (sdk#3), which
// carries season and episode numbers but no node ids; playback state is keyed by
// node (platform#26). A series' children are its seasons and a season's children
// its episodes, each carrying its number as NaturalOrder, so the tree maps
// (season, episode) back to the node the position is stored under. It reads only
// the selected season — one season walk and one batched state read — because
// that is all the rows show.
//
// Every failure leaves the view as it was rather than returning an error: an
// unmarked episode row is still a row, and a detail screen that cannot read
// progress should lose its bars, not its episodes.
func (s *Service) fillSeasonProgress(ctx context.Context, caller v1.Caller, seriesNode v1.NodeID, view *seasonView) {
	seasons, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{
		Caller: caller, NodeID: seriesNode, WithChildren: true,
	})
	if err != nil {
		telemetry.From(ctx).For("screens").Warn("reading season tree for episode progress failed",
			telemetry.Identifier("series", string(seriesNode)), telemetry.Err(err))
		return
	}
	var seasonNode v1.NodeID
	for _, c := range seasons.Children {
		if c.Kind == v1.NodeContainer && int(c.NaturalOrder) == view.selected {
			seasonNode = c.ID
			break
		}
	}
	if seasonNode == "" {
		return
	}

	eps, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{
		Caller: caller, NodeID: seasonNode, WithChildren: true,
	})
	if err != nil {
		telemetry.From(ctx).For("screens").Warn("reading episode nodes for episode progress failed",
			telemetry.Identifier("season", string(seasonNode)), telemetry.Err(err))
		return
	}
	byNumber := make(map[int]v1.NodeID, len(eps.Children))
	ids := make([]v1.NodeID, 0, len(eps.Children))
	view.nodes = make(map[v1.NodeID]int, len(eps.Children))
	for _, ep := range eps.Children {
		if ep.ItemType != v1.ItemEpisode {
			continue
		}
		byNumber[int(ep.NaturalOrder)] = ep.ID
		view.nodes[ep.ID] = int(ep.NaturalOrder)
		ids = append(ids, ep.ID)
	}
	if len(ids) == 0 {
		return
	}

	states, err := s.content.ListPlaybackStates(ctx, v1.ListPlaybackStatesQuery{Caller: caller, NodeIDs: ids})
	if err != nil {
		telemetry.From(ctx).For("screens").Warn("reading playback states for episode progress failed",
			telemetry.Err(err))
		return
	}
	view.states = make(map[int]v1.PlaybackState, len(byNumber))
	for num, id := range byNumber {
		if st, ok := states.States[id]; ok {
			view.states[num] = st
		}
	}

	// What each episode's release actually is, for the quality pill on its row.
	// One read per episode of the shown season only; a season nobody is
	// looking at costs nothing.
	view.quality = make(map[int]string, len(byNumber))
	for num, id := range byNumber {
		parts, partErr := s.content.ListNodeParts(ctx, app.ListNodePartsQuery{Caller: caller, NodeID: id})
		if partErr != nil || len(parts.Parts) == 0 {
			continue
		}
		if q := factsFor(parts.Parts[0]).qualityPill(); q != "" {
			view.quality[num] = q
		}
	}
}

// episodesSection builds the series' episode browser: the season control and a
// count beside the heading, over the selected season's rows.
func (s *Service) episodesSection(ref v1.ContentRef, m v1.ContentMetadata, season seasonView) ui.El {
	seasonEntries := make([]map[string]any, 0, len(season.order))
	for _, n := range season.order {
		seasonEntries = append(seasonEntries, map[string]any{
			"id":     strconv.Itoa(n),
			"label":  fmt.Sprintf("Season %d", n),
			"action": ui.Navigate(screenDetail, map[string]any{paramRef: refInput(ref), paramSeason: strconv.Itoa(n)}),
		})
	}
	selector := ui.Component("SeasonSelector",
		ui.Prop("seasons", seasonEntries), ui.Prop("selected", strconv.Itoa(season.selected)))

	rows := make([]ui.El, 0, len(season.episodes))
	for _, e := range season.episodes {
		els := []ui.El{
			ui.Index(episodeCode(e)),
			ui.When(e.Overview != "", ui.Overview(e.Overview)),
			ui.When(e.Thumbnail != "", ui.Thumbnail(s.art(e.Thumbnail))),
			ui.When(e.RuntimeMinutes > 0, ui.Runtime(fmt.Sprintf("%d min", e.RuntimeMinutes))),
			ui.When(season.quality[e.Episode] != "", ui.Quality(season.quality[e.Episode])),
		}
		if st, ok := season.states[e.Episode]; ok {
			if st.Finished {
				els = append(els, ui.Watched(true), ui.Progress(1))
			} else if p := watchedFraction(st); p > 0 {
				els = append(els, ui.Progress(p))
			}
		}
		rows = append(rows, ui.EpisodeRow(e.Title, els...))
	}

	// The count beside the heading, as the design writes it: how many episodes
	// this season has and when the series is from.
	subtitle := fmt.Sprintf("%d %s", len(season.episodes), plural(len(season.episodes), "episode"))
	if y := yearLabel(m.Year); y != "" {
		subtitle += " · " + y
	}

	// The season control rides the heading line, as the design draws it — beside
	// the title and before the count — rather than as the first thing in the
	// list. A control that scrolls away with the rows it filters is a control a
	// viewer has to scroll back up to reach.
	return ui.Section("Episodes", ui.Header(selector), ui.Subtitle(subtitle),
		ui.Stack("vertical", 2, rows...))
}

// episodeCode is an episode's place in its series, "S2 E7".
func episodeCode(e v1.EpisodePreview) string {
	return fmt.Sprintf("S%d E%d", e.Season, e.Episode)
}

// watchedFraction is how far through an episode this viewer got, 0 when the
// duration is unknown — a position with no duration is a number with no
// denominator, and a bar drawn from one would be a guess.
func watchedFraction(st v1.PlaybackState) float64 {
	if st.Duration <= 0 || st.Position <= 0 {
		return 0
	}
	f := st.Position.Seconds() / st.Duration.Seconds()
	if f > 1 {
		return 1
	}
	return f
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

// libraryDetail renders a materialised node: its header, and its direct children
// as cards that open their own detail (one level per screen, since the tree is
// of variable depth — platform#9). A film's child is its feature item; a series'
// children are its seasons.
func (s *Service) libraryDetail(ctx context.Context, caller v1.Caller, nodeID string, params map[string]any) (sdui.Node, error) {
	// The season the screen is on, read here so the query fetches that season's
	// episodes and no others.
	season := 0
	if sv := stringParam(params, paramSeason); sv != "" {
		if n, err := strconv.Atoi(sv); err == nil {
			season = n
		}
	}
	res, err := s.content.GetLibraryDetail(ctx, app.GetLibraryDetailQuery{
		Caller: caller, NodeID: v1.NodeID(nodeID), Season: season,
	})
	if err != nil {
		return nil, err
	}
	n := res.Node

	if !res.HasMetadata {
		// Never enriched — materialised before platform#62, or by a provider that
		// has never been reachable since. It still has a title, artwork and a
		// tree, so it renders as those rather than as an error or an apology;
		// the next maintenance run fills the rest in.
		return s.structuralDetail(n, res.Children), nil
	}

	m := res.Metadata
	// The tree is the authority on what episodes exist, and the stored document
	// deliberately carries none (platform#62). Projecting them back here is what
	// lets one renderer serve both planes.
	m.Episodes = app.EpisodesFromTree(res.Season, res.Episodes)

	// The node fills what the document does not. Artwork is the case that
	// matters: it is stored on the node (platform#45) and re-ranked by the artwork
	// pass (platform#47), so the node's copy is the better one and the document's
	// is what its metadata provider happened to carry.
	if p := n.Artwork.Poster; p != "" {
		m.Poster = p
	}
	if b := n.Artwork.Backdrop; b != "" {
		m.Backdrop = b
	}
	if l := n.Artwork.Logo; l != "" {
		m.Logo = l
	}
	if m.Title == "" {
		m.Title = n.Title
	}

	// The ref the document was fetched under, so the actions that need one — a
	// franchise rail, a trailer — still work. Its media type comes from the node
	// when the document has none, because the graph's is the canonical one
	// (platform#11) and the kicker reads it.
	ref := m.Ref
	if ref.MediaType == "" {
		ref.MediaType = n.MediaType
	}

	return s.renderDetail(ctx, caller, ref, app.PreviewContentResult{
		Metadata: m, InLibrary: true, NodeID: n.ID,
	}, app.SeasonNumbers(res.Children), params)
}

// structuralDetail is what a node with no stored description renders as: what
// the graph itself holds, which is a title, its artwork and its contents.
//
// It is the floor beneath the platform#62 detail. It draws the children's
// posters; without them the fallback is a grid of blank cards.
func (s *Service) structuralDetail(n v1.Node, children []v1.Node) sdui.Node {
	heroEls := []ui.El{
		ui.Title(n.Title),
		ui.When(n.Artwork.Backdrop != "", ui.Backdrop(s.art(n.Artwork.Backdrop))),
		ui.When(n.Artwork.Logo != "", ui.Logo(s.art(n.Artwork.Logo))),
		ui.When(n.Artwork.Poster != "", ui.Poster(s.art(n.Artwork.Poster))),
	}
	if k := mediaTypeWord(string(n.MediaType)); k != "" {
		heroEls = append(heroEls, ui.Kicker(k))
	}
	body := []ui.El{ui.Slot("bleed", ui.Component("DetailHero", heroEls...))}

	cards := make([]ui.El, 0, len(children))
	for _, c := range children {
		cards = append(cards, ui.PosterCard(c.Title, string(c.MediaType),
			ui.When(c.Artwork.Poster != "", ui.Poster(s.art(c.Artwork.Poster))),
			ui.OnTap(ui.Navigate(screenDetail, map[string]any{paramNodeID: string(c.ID)}))))
	}
	if len(cards) > 0 {
		body = append(body, ui.Section("Contents",
			ui.Grid(ui.MinColumnWidth(196), ui.Group(cards...))))
	}
	return ui.Screen(ui.Title(n.Title), ui.Group(body...)).Build()
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
// does: a related item is a virtual item (platform#18), and opening one is a read.
//
// The heading carries a pinned link onward into a catalog of the same kind. It
// is pinned rather than hover-revealed because a rail's heading is the only way
// into a full catalog and no remote control has a pointer to reveal it with.
func (s *Service) relatedRail(ctx context.Context, caller v1.Caller, title string, items []v1.RelatedItem, self v1.ContentRef) ui.El {
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
	// 190 rather than the browse rails' 196: this rail sits in the detail's
	// 1320 content column, not the home page's full bleed.
	els := []ui.El{ui.Carousel(ui.ItemWidth(190), ui.Group(cards...))}
	if label, action, ok := s.browseAction(ctx, caller, self); ok {
		els = append(els, ui.ActionLabel(label), ui.PinAction(true), ui.OnTap(action))
	}
	return ui.Section(title, els...)
}

// browseAction is the "Browse series →" link a related rail's heading carries —
// a catalog of the same native type as the title being described.
//
// It resolves a real catalog rather than linking to the collection index,
// because "browse series" that opens a list of every catalog including the film
// ones is not the promise the label makes. When no registered module offers a
// catalog of this kind there is no link, which is the honest answer for an
// install whose only source is a metadata provider.
func (s *Service) browseAction(ctx context.Context, caller v1.Caller, ref v1.ContentRef) (string, ui.Action, bool) {
	if ref.NativeType == "" {
		return "", ui.Action{}, false
	}
	res, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil || len(res.Catalogs) == 0 {
		return "", ui.Action{}, false
	}
	matches := make([]app.ModuleCatalog, 0, len(res.Catalogs))
	for _, c := range res.Catalogs {
		if strings.EqualFold(c.Catalog.NativeType, ref.NativeType) {
			matches = append(matches, c)
		}
	}
	if len(matches) == 0 {
		return "", ui.Action{}, false
	}
	// Stable across renders: the module list is a map fan-out and its order is
	// not guaranteed, so a link that picked "the first" would wander between
	// catalogs on successive views of the same screen.
	sort.SliceStable(matches, func(i, j int) bool {
		if matches[i].ModuleID != matches[j].ModuleID {
			return matches[i].ModuleID < matches[j].ModuleID
		}
		return matches[i].Catalog.ID < matches[j].Catalog.ID
	})
	pick := matches[0]
	label := "Browse " + strings.ToLower(mediaTypeWord(string(ref.MediaType)))
	if label == "Browse " {
		label = "Browse all"
	}
	return label, ui.Navigate(screenCatalog, map[string]any{
		paramModuleID:   pick.ModuleID,
		paramCatalogID:  pick.Catalog.ID,
		paramNativeType: pick.Catalog.NativeType,
		paramTitle:      pick.Catalog.Name,
	}), true
}
