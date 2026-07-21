// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strconv"

	sdui "github.com/mosaic-media/sdui/sdui"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The screen builders share a handful of shapes — a titled page, an
// empty-state page, a titled grid or carousel of cards. These constructors name
// those shapes once so the builders read as intent rather than nesting.

// screen builds a titled Screen over the given body nodes.
func screen(title string, body ...sdui.Node) sdui.Node {
	return sdui.Screen(sdui.Prop("title", title), sdui.Child(body...))
}

// emptyScreen builds a titled Screen whose whole body is one EmptyState.
func emptyScreen(title, icon, message string) sdui.Node {
	return screen(title, sdui.EmptyState(icon, message))
}

// gridScreen builds a titled Screen whose body is a responsive grid of cards.
func gridScreen(title string, cards ...sdui.Node) sdui.Node {
	return screen(title, sdui.Grid(sdui.Child(cards...)))
}

// carouselSection is a titled band holding a horizontal snap-scrolling rail of
// cards — one catalog row on the home screen.
func carouselSection(title string, cards ...sdui.Node) sdui.Node {
	return sdui.Section(title, sdui.Child(sdui.Carousel(sdui.Child(cards...))))
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

// refFromParam reads a ContentRef out of a screen's ref param (a decoded JSON
// object) — the inverse of refInput.
func refFromParam(m map[string]any) v1.ContentRef {
	get := func(k string) string { s, _ := m[k].(string); return s }
	return v1.ContentRef{
		Provider: get("provider"), NativeID: get("nativeId"), NativeType: get("nativeType"),
		MediaType: v1.MediaType(get("mediaType")), ExternalScheme: get("externalScheme"), ExternalID: get("externalId"),
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
