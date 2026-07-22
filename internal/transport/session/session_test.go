// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"encoding/json"
	"testing"
)

// TestSafeAction guards the action name a client submits: it must be a plain
// identifier the dispatch switch can map, and anything else is rejected before
// it reaches a service.
func TestSafeAction(t *testing.T) {
	valid := []string{"importContent", "configureModule", "a", "a1", "snake_case", "camelCase9"}
	for _, s := range valid {
		if !safeAction(s) {
			t.Errorf("safeAction(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "1leading", "has space", "paren()", "brace{}", "dash-name", "dot.name", "quote\"", "semi;drop"}
	for _, s := range invalid {
		if safeAction(s) {
			t.Errorf("safeAction(%q) = true, want false", s)
		}
	}
}

// TestImportRefFromInput proves the importContent action envelope decodes to the
// ContentRef the application command takes — the action ABI (ADR 0029) the
// dispatch reads off the wire, unchanged by which client sent it.
func TestImportRefFromInput(t *testing.T) {
	input := []byte(`{"ref":{"provider":"stremio","nativeId":"tt0111161","nativeType":"movie","mediaType":"movie","externalScheme":"imdb","externalId":"tt0111161"}}`)
	ref, err := importRefFromInput(input)
	if err != nil {
		t.Fatalf("importRefFromInput: %v", err)
	}
	if ref.Provider != "stremio" || ref.NativeID != "tt0111161" || ref.NativeType != "movie" {
		t.Fatalf("ref = %+v, want provider/nativeId/nativeType populated", ref)
	}
	if string(ref.MediaType) != "movie" || ref.ExternalScheme != "imdb" || ref.ExternalID != "tt0111161" {
		t.Fatalf("ref external fields = %+v, want imdb/tt0111161", ref)
	}

	if _, err := importRefFromInput([]byte(`not json`)); err == nil {
		t.Fatal("importRefFromInput(invalid json) = nil error, want InvalidArgument")
	}
	// An empty envelope is not an error — it yields an empty ref the command
	// then rejects on shape.
	if _, err := importRefFromInput(nil); err != nil {
		t.Fatalf("importRefFromInput(nil) = %v, want nil", err)
	}
}

// TestConfigureFromInput proves the configureModule action envelope decodes to a
// module id and an opaque settings document carried through verbatim (ADR 0021).
func TestConfigureFromInput(t *testing.T) {
	input := []byte(`{"moduleId":"stremio","settings":{"addons":["https://example.com/manifest.json"]}}`)
	id, settings, err := configureFromInput(input)
	if err != nil {
		t.Fatalf("configureFromInput: %v", err)
	}
	if id != "stremio" {
		t.Fatalf("moduleId = %q, want stremio", id)
	}
	// Settings is passed through opaquely; re-decode to confirm it round-trips.
	var doc map[string]any
	if err := json.Unmarshal(settings, &doc); err != nil {
		t.Fatalf("settings not valid JSON: %v", err)
	}
	if _, ok := doc["addons"]; !ok {
		t.Fatalf("settings = %s, want an addons key", settings)
	}

	// Absent settings yields nil, which the command stores as an empty document
	// rather than the string "null".
	id, settings, err = configureFromInput([]byte(`{"moduleId":"stremio","settings":null}`))
	if err != nil {
		t.Fatalf("configureFromInput(null settings): %v", err)
	}
	if id != "stremio" || settings != nil {
		t.Fatalf("id=%q settings=%v, want stremio/nil", id, settings)
	}

	if _, _, err := configureFromInput([]byte(`{`)); err == nil {
		t.Fatal("configureFromInput(invalid json) = nil error, want InvalidArgument")
	}
}

// TestDecodeParams proves screen params decode from their JSON object, and that
// an absent or malformed bag decodes to nil (read as "no params" by builders).
func TestDecodeParams(t *testing.T) {
	if got := decodeParams(nil); got != nil {
		t.Fatalf("decodeParams(nil) = %v, want nil", got)
	}
	if got := decodeParams([]byte(`nope`)); got != nil {
		t.Fatalf("decodeParams(invalid) = %v, want nil", got)
	}
	got := decodeParams([]byte(`{"ref":{"provider":"stremio"},"season":"2"}`))
	if got["season"] != "2" {
		t.Fatalf("decodeParams season = %v, want \"2\"", got["season"])
	}
	if _, ok := got["ref"].(map[string]any); !ok {
		t.Fatalf("decodeParams ref = %v, want a nested object", got["ref"])
	}
}

// TestErrorNode proves a failed render produces the ErrorState UINode the
// content region shows (ADR 0029's error surface).
func TestErrorNode(t *testing.T) {
	node := errorNode("boom")
	if node.GetType() != "ErrorState" {
		t.Fatalf("error node type = %q, want ErrorState", node.GetType())
	}
	props := node.GetProps().AsMap()
	if props["category"] != "Unavailable" || props["message"] != "boom" {
		t.Fatalf("error node props = %v, want Unavailable/boom", props)
	}
}

// TestInvokeToast proves the success confirmation reflects the action rather
// than assuming a library import.
func TestInvokeToast(t *testing.T) {
	cases := map[string]string{
		"importContent":   "Added to library",
		"configureModule": "Settings saved",
		"somethingElse":   "Done",
	}
	for action, want := range cases {
		if got := invokeToast(action); got != want {
			t.Errorf("invokeToast(%q) = %q, want %q", action, got, want)
		}
	}
}
