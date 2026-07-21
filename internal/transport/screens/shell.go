// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	sdui "github.com/mosaic-media/mosaic-sdui/sdui"
)

// shellScreen is the server-emitted application frame (ADR 0031): the nav rail
// and top bar. The Shell renders this and fills its content region with the
// current screen; it owns no chrome of its own. It is static for now — a live
// session (ADR 0032) will push shell changes over the socket.
func (s *Service) shellScreen() (sdui.Node, error) {
	return sdui.Component("AppShell",
		sdui.Prop("title", "Mosaic"),
		sdui.Slot("nav",
			navItem("Home", "home", screenHome),
			navItem("Collections", "list", screenCollections),
			navItem("Settings", "settings", screenSettings),
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
