// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strconv"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Small helpers the screen builders share — ref (de)serialization and param
// reads. The UI shapes themselves are composed inline with the ui package.

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

// intParam reads a whole-number screen param.
//
// A param arrives as JSON, where every number is a float64, and a page that
// came back as 1.0000000001 would page nothing. Anything that is not a number
// is zero, which for a page index is the first page — the right failure for a
// malformed cursor is the beginning, not an empty screen.
func intParam(params map[string]any, key string) int {
	if params == nil {
		return 0
	}
	switch v := params[key].(type) {
	case float64:
		return int(v)
	case int:
		return v
	default:
		return 0
	}
}

func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	s, _ := params[key].(string)
	return s
}

// notFoundScreen is what a deep link that names no screen resolves to.
//
// It is a screen, not an error. Returning NotFound for an unknown name leaves
// the session transport rendering a raw error node, so a stale bookmark or a
// mistyped URL puts "no screen named …" in the content region — a stack trace
// wearing a sentence. A wrong route is an ordinary thing for a user to do and
// deserves a way out rather than a diagnosis.
//
// The unauthenticated half of the same question is not answered here: a deep
// link opened without a session never reaches this function, because there is no
// session to render it into. That path belongs to sign-in.
func notFoundScreen() sdui.Node {
	return ui.Screen(
		ui.EmptyState(emptyIconNotFound, "This page isn’t in the library.",
			ui.Message("The link may have moved, or the item was removed from your server."),
			ui.ActionSlot(
				ui.Button("Back to home", "primary", ui.IconName("home"),
					ui.OnTap(ui.Navigate(screenHome, nil))),
				ui.Button("Search the library", "secondary", ui.IconName("search"),
					ui.OnTap(ui.Navigate(screenSearch, nil))),
			),
		),
	).Build()
}
