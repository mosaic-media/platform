// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"errors"
	"strings"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The two chromes the app wears (platform#21).
//
// Mosaic is two rooms in one building. The media side is a full-bleed hero with
// floating glass pills over it and no bar to speak of; the administrative side —
// settings, the extension store, diagnostics — is a working surface with a solid
// bar, a way back and a trail saying where you are. The design draws them
// differently on purpose, and wearing the media chrome over settings made the
// second look like a page of the first.
const (
	chromeMedia = "media"
	chromeAdmin = "admin"
)

// chromeFor is which frame a screen wears.
//
// Decided here rather than by each screen, because the shell is one tree and a
// screen that could choose its own chrome could disagree with the screen beside
// it in the same region.
func chromeFor(screen string) string {
	switch screen {
	case screenSettings, screenExtensions, screenLogs, screenTraces, screenTrace, screenMetrics:
		return chromeAdmin
	default:
		return chromeMedia
	}
}

// breadcrumbFor is the trail after the brand in the admin chrome.
func breadcrumbFor(screen string) string {
	switch screen {
	case screenSettings, screenExtensions:
		return "/ Settings"
	case screenLogs:
		return "/ Settings / Logs"
	case screenTraces, screenTrace:
		return "/ Settings / Traces"
	case screenMetrics:
		return "/ Settings / Metrics"
	default:
		return ""
	}
}

// shellScreen is the server-emitted application frame (platform#21): the chrome the
// current screen wears, and the content region it renders into.
//
// It takes the route because the chrome depends on it: a frame emitted once per
// session leaves the settings side of the app wearing the media side's floating
// pills, with no moment at which anything could decide otherwise. It is re-pushed
// when a navigation changes the chrome — not on every navigation, because the
// tree is identical for every screen on the same side and re-sending it would be
// a payload per tap for no change.
func (s *Service) shellScreen(ctx context.Context, caller v1.Caller, screen string) (sdui.Node, error) {
	chrome := chromeFor(screen)

	els := []ui.El{
		ui.Title("Mosaic"),
		ui.Chrome(chrome),
		// Desktop right cluster. It is the same on both sides: whoever you are
		// signed in as is the one thing that does not change between rooms.
		s.accountMenu(ctx, caller),
	}

	if chrome == chromeAdmin {
		// No nav rail and no search: the administrative side has its own nav in
		// the screen, and a search box over it would search the library from a
		// room the library is not in.
		els = append(els, ui.Breadcrumb(breadcrumbFor(screen)))
		return ui.Component("AppShell", els...).Build(), nil
	}

	els = append(els,
		ui.Slot("nav",
			navItem("Home", "home", screenHome),
			navItem("Search", "search", screenSearch),
			// The install's own shelf, above the sources it was filled from.
			// Library and Collections are adjacent and are not the same room:
			// one is what this household owns, the other is what a module
			// offers.
			navItem("Library", "grid", screenLibrary),
			navItem("Collections", "list", screenCollections),
			navItem("Settings", "settings", screenSettings),
		),
		// The search bar owns the centre of the top bar and is always present.
		// Typing takes over the content region (a live input); clearing it
		// returns to the current screen.
		ui.Slot("topbar",
			ui.Component("SearchBar", ui.Prop("placeholder", "Search for anime, movies, shows…")),
		),
	)
	return ui.Component("AppShell", els...).Build(), nil
}

// accountMenu is the frame's who-you-are cluster.
//
// It renders per caller. A shell that says the same thing to four people —
// a fixed initial, a fixed name — is the failure multi-user exists to end, and
// it is invisible on an install with one account.
//
// Sign out lives here and nowhere else. platform#58's device list ends other
// devices and deliberately draws no control for the one you are looking
// through, on the grounds that signing yourself out belongs on its own
// affordance rather than inside a list of devices. This is that affordance: the
// account cluster, which is the only chrome present on every screen, so handing
// a shared device over never means finding a settings page first.
func (s *Service) accountMenu(ctx context.Context, caller v1.Caller) ui.El {
	initial, name := "?", ""
	// A Service with no query surface is a frame test, not a signed-in person.
	// It draws the cluster without a name rather than refusing to draw a shell.
	if res, err := s.currentUser(ctx, caller); err == nil {
		name = res.User.DisplayName
		if name == "" {
			name = res.User.Username
		}
		initial = initialOf(name)
	}

	items := []any{
		map[string]any{"label": "Collections", "icon": "list", "action": ui.Navigate(screenCollections, nil)},
		// Where a person's own viewing lives (platform#59). It is in the account
		// cluster rather than in the nav rail because it is about you rather than
		// about the library — the nav is the four rooms everybody shares, and this
		// is the one screen no two people on the server see the same.
		map[string]any{"label": "Your history", "icon": "play", "action": ui.Navigate(screenHistory, nil)},
		map[string]any{"label": "Settings", "icon": "settings", "action": ui.Navigate(screenSettings, nil)},
		// Tone danger because it ends the session rather than opening a screen,
		// and because the row above it is "Settings" — two adjacent rows that
		// look alike and do very different things is how somebody signs out of a
		// shared television by accident.
		map[string]any{"label": "Sign out", "icon": "error", "tone": "danger", "action": ui.Invoke(signOutAction, nil)},
	}
	label := "Account"
	if name != "" {
		label = "Account: " + name
	}
	return ui.Slot("account",
		ui.Component("Menu",
			ui.Prop("initial", initial),
			// The accessible name carries who is signed in, because the visible
			// trigger is a single letter and "A" announced on its own says
			// nothing at all.
			ui.Prop("label", label),
			ui.Prop("items", items),
		),
	)
}

// currentUser reads who is asking, or an error when there is nothing to ask.
func (s *Service) currentUser(ctx context.Context, caller v1.Caller) (app.GetCurrentUserResult, error) {
	if s.content == nil {
		return app.GetCurrentUserResult{}, errNoQuerySurface
	}
	return s.content.GetCurrentUser(ctx, app.GetCurrentUserQuery{Caller: caller})
}

// errNoQuerySurface is what a Service built without query services answers.
var errNoQuerySurface = errors.New("this Service has no query surface")

// initialOf is the letter the account circle shows.
//
// The first rune rather than the first byte: a display name beginning with a
// multi-byte character would otherwise render as half of one, which is the
// ordinary case for most of the world and never the case in a test written in
// English.
func initialOf(name string) string {
	for _, r := range strings.TrimSpace(name) {
		return strings.ToUpper(string(r))
	}
	return "?"
}

// navItem builds one sidebar navigation button that navigates to a screen.
func navItem(label, icon, screen string) *ui.Element {
	return ui.Component("NavItem",
		ui.Prop("label", label), ui.Prop("icon", icon), ui.Prop("screen", screen),
		ui.OnTap(ui.Navigate(screen, nil)),
	)
}

// ShellChromeFor is what the session transport asks to decide whether a
// navigation changed the frame, so it re-pushes the shell only when it did.
func ShellChromeFor(screen string) string { return chromeFor(screen) }
