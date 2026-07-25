// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"
)

// The two chromes the app wears (ADR 0031).
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
	case screenSettings, screenExtensions, screenLogs, screenTraces, screenTrace:
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
	default:
		return ""
	}
}

// shellScreen is the server-emitted application frame (ADR 0031): the chrome the
// current screen wears, and the content region it renders into.
//
// It takes the route, which it did not before. The frame used to be emitted once
// per session and never again, which is why the settings side of the app wore
// the media side's floating pills: there was no moment at which anything could
// have decided otherwise. It is re-pushed when a navigation changes the chrome —
// not on every navigation, because the tree is identical for every screen on the
// same side and re-sending it would be a payload per tap for no change.
func (s *Service) shellScreen(screen string) (sdui.Node, error) {
	chrome := chromeFor(screen)

	els := []ui.El{
		ui.Title("Mosaic"),
		ui.Chrome(chrome),
		// Desktop right cluster. It is the same on both sides: whoever you are
		// signed in as is the one thing that does not change between rooms.
		ui.Slot("account",
			ui.Component("Menu",
				ui.Prop("initial", "A"),
				ui.Prop("label", "Account"),
				ui.Prop("items", []any{
					map[string]any{"label": "Collections", "icon": "list", "action": ui.Navigate(screenCollections, nil)},
					map[string]any{"label": "Settings", "icon": "settings", "action": ui.Navigate(screenSettings, nil)},
				}),
			),
		),
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
			navItem("Collections", "list", screenCollections),
			navItem("Settings", "settings", screenSettings),
		),
		// The search bar owns the centre of the top bar and is always present, so
		// there is no Search nav item. Typing takes over the content region (a live
		// `input`); clearing it returns to the current screen.
		ui.Slot("topbar",
			ui.Component("SearchBar", ui.Prop("placeholder", "Search for anime, movies, shows…")),
		),
	)
	return ui.Component("AppShell", els...).Build(), nil
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
