// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"
)

// shellScreen is the server-emitted application frame (ADR 0031): the nav rail
// and top bar. The Shell renders this and fills its content region with the
// current screen; it owns no chrome of its own. It is static for now — a live
// session (ADR 0032) will push shell changes over the socket.
func (s *Service) shellScreen() (sdui.Node, error) {
	return ui.Component("AppShell",
		ui.Title("Mosaic"),
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
		// Desktop right cluster: just the avatar menu, so the bar stays clean —
		// brand (= Home) · centred search · avatar. Home is the brand; Collections
		// and Settings live in the menu. On mobile these are the bottom tab bar
		// (the `nav` slot), so this cluster is hidden.
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
	).Build(), nil
}

// navItem builds one sidebar navigation button that navigates to a screen.
func navItem(label, icon, screen string) *ui.Element {
	return ui.Component("NavItem",
		ui.Prop("label", label), ui.Prop("icon", icon), ui.Prop("screen", screen),
		ui.OnTap(ui.Navigate(screen, nil)),
	)
}
