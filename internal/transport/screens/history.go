// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Watch history — the per-user pass over a shared library (ADR 0103).
//
// **A household never shares this screen.** The library beneath it is one
// object graph and everybody sees the same titles; what each person did with it
// is theirs, keyed by (user, node) since playback state landed. The query takes
// no user parameter, so there is no version of this screen that shows somebody
// else's — which is a property of the read rather than of the screen, and is
// the right place for it.
//
// It is the third surface over the same rows and it is not the other two. A
// resume offset answers "where am I in this"; the continue-watching rail
// answers "what should I pick back up", and deliberately omits what has been
// finished. This answers "what have I watched", which without it nothing in
// Mosaic could.

// historyScreen renders what this viewer has watched, most recent first.
func (s *Service) historyScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	res, err := s.content.ListWatchHistory(ctx, app.ListWatchHistoryQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	if len(res.Items) == 0 {
		return ui.Screen(
			ui.Title("Your history"),
			ui.Subtitle("Everything you have watched on this server."),
			ui.EmptyState("play",
				"Nothing here yet",
				ui.Message("What you watch shows up here, and only here — nobody else on this "+
					"server sees it."),
			),
		).Build(), nil
	}

	// One indexed read per row for the Work the item belongs to, run
	// concurrently for the same reason the continue-watching rail does it: a
	// hundred sequential reads is a hundred round trips, and a row whose read
	// fails should drop out rather than take the screen with it.
	cards := make([]ui.El, len(res.Items))
	var wg sync.WaitGroup
	for i, item := range res.Items {
		wg.Add(1)
		go func() {
			defer wg.Done()
			cards[i] = s.historyCard(ctx, caller, item)
		}()
	}
	wg.Wait()

	out := make([]ui.El, 0, len(cards))
	for _, c := range cards {
		if c != nil {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		// Every row's Work read failed, which is not the same as an empty
		// history and must not render as one — "you have watched nothing" is a
		// statement about the person, and this is a statement about the server.
		telemetry.From(ctx).For("screens").Warn(
			"every watch-history row failed to resolve its work; rendering an error rather than an empty history")
		return ui.Screen(
			ui.Title("Your history"),
			ui.ErrorState(string(contracts.Unavailable),
				ui.Title("Your history could not be loaded"),
				ui.Message("The items are still there. Try again in a moment.")),
		).Build(), nil
	}

	return ui.Screen(
		ui.Title("Your history"),
		ui.Subtitle(fmt.Sprintf("%d %s, most recent first.", len(out), plural(len(out), "title"))),
		ui.Section("",
			ui.Grid(ui.MinColumnWidth(196), ui.Group(out...))),
	).Build(), nil
}

// historyCard renders one watched item: the Work's poster, what it was, and how
// far through it got.
//
// It opens the detail screen rather than resuming. That is the difference
// between this and a continue-watching tile, and it is deliberate: a rail is for
// picking something back up, and a history is for finding something again —
// including something already finished, which has nothing to resume.
func (s *Service) historyCard(ctx context.Context, caller v1.Caller, item app.WatchedItem) ui.El {
	work, err := s.content.GetContentNode(ctx, v1.GetContentNodeQuery{Caller: caller, NodeID: item.Node.WorkID})
	if err != nil {
		return nil
	}

	els := make([]ui.El, 0, 6)
	if p := work.Node.Artwork.Poster; p != "" {
		els = append(els, ui.Poster(s.art(p)))
	}
	// Name the episode under the series title. A film's item adds nothing the
	// title has not already said.
	if item.Node.ItemType == v1.ItemEpisode && item.Node.Title != "" {
		els = append(els, ui.Subtitle(item.Node.Title))
	}
	if item.State.Finished {
		// A finished item shows a mark rather than a full progress bar. A bar at
		// 100% and a bar at 97% look the same from across a room, and only one of
		// them means the thing was watched.
		els = append(els, ui.BadgeText("Watched"))
	} else if f := progressFraction(item.State); f > 0 {
		els = append(els, ui.Progress(f))
		if left := remainingLabel(item.State); left != "" {
			els = append(els, ui.ProgressLabel(left))
		}
	}
	if when := watchedWhen(item.State.UpdatedAt, s.now()); when != "" {
		els = append(els, ui.Meta(when))
	}
	els = append(els, ui.OnTap(ui.Navigate(screenDetail, map[string]any{
		paramNodeID: string(work.Node.ID),
	})))
	return ui.PosterCard(work.Node.Title, string(work.Node.MediaType), els...)
}

// watchedWhen says when, in the coarsest unit that is still true.
//
// Coarse on purpose. A history is for recognising something, and "yesterday"
// does that better than a timestamp; the exact minute somebody watched
// something is a fact about them that this screen has no reason to publish to
// whoever is standing behind them.
func watchedWhen(at, now time.Time) string {
	if at.IsZero() {
		return ""
	}
	switch elapsed := now.Sub(at); {
	case elapsed < 0:
		// A clock that disagrees with the database is not something a card should
		// try to phrase.
		return ""
	case elapsed < time.Hour:
		return "Just now"
	case elapsed < 24*time.Hour:
		return "Today"
	case elapsed < 48*time.Hour:
		return "Yesterday"
	case elapsed < 7*24*time.Hour:
		return strconv.Itoa(int(elapsed.Hours())/24) + " days ago"
	default:
		return at.Format("2 January 2006")
	}
}

// now is the clock the history reads. It is a field on the Service rather than
// a call to time.Now so a test can assert "Yesterday" without waiting a day.
func (s *Service) now() time.Time {
	if s.clock == nil {
		return time.Now()
	}
	return s.clock()
}
