// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strconv"

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

// stringParam reads a string screen param, tolerating an absent or non-string
// value.
func stringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	s, _ := params[key].(string)
	return s
}
