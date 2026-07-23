// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"encoding/json"
	"testing"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
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

// TestSilentActionsAreOnlyThePeriodicOnes guards a bug that would have shipped
// looking like a feature request.
//
// Invoke's default is to confirm and re-render, which is right for something a
// person pressed. A player reports its position every fifteen seconds, so under
// that default a playing film would have raised a "Done" toast four times a
// minute and re-rendered the screen underneath itself each time — tearing down
// the very player that was reporting.
func TestSilentActionsAreOnlyThePeriodicOnes(t *testing.T) {
	if !silentAction("reportProgress") {
		t.Error("reportProgress would toast and re-render on every position report")
	}
	// Everything a person presses still confirms. setWatched especially: it is
	// the one whose whole visible effect is the re-render.
	for _, action := range []string{"importContent", "configureModule", "setPreference", "setWatched", "playPart"} {
		if silentAction(action) {
			t.Errorf("%q was made silent; a user action needs its confirmation", action)
		}
	}
}

// TestProgressEnvelopeDecoding covers the action ABI the Player emits, including
// the two values a media element genuinely produces for an unknown length.
func TestProgressEnvelopeDecoding(t *testing.T) {
	env, err := progressFromInput([]byte(`{"nodeId":"n-1","partId":"p-1","position":91.5,"duration":5400,"final":true}`))
	if err != nil {
		t.Fatalf("progressFromInput: %v", err)
	}
	if env.NodeID != "n-1" || env.PartID != "p-1" || !env.Final {
		t.Errorf("decoded %+v", env)
	}
	cmd := env.command(v1.Caller{Session: "s-1"})
	if cmd.Position != 91500*time.Millisecond {
		t.Errorf("position = %v, want 91.5s", cmd.Position)
	}
	if cmd.Duration != 90*time.Minute {
		t.Errorf("duration = %v, want 90m", cmd.Duration)
	}

	// A report with no node has nothing to be a position of.
	if _, err := progressFromInput([]byte(`{"position":10}`)); err == nil {
		t.Error("a progress report with no node id was accepted")
	}
	// Negative values are not a player's honest output; they are a bug
	// somewhere, and storing one would put an item at a position no seek can
	// reach.
	if _, err := progressFromInput([]byte(`{"nodeId":"n-1","position":-5}`)); err == nil {
		t.Error("a negative position was accepted")
	}
}

// TestSecondsRoundsToTheStoredPrecision pins the float-to-Duration conversion at
// the millisecond the store keeps. A float carried further than the column's
// precision is a value that compares unequal to itself across a round trip.
func TestSecondsRoundsToTheStoredPrecision(t *testing.T) {
	if got := seconds(12.3456); got != 12345*time.Millisecond {
		t.Errorf("seconds(12.3456) = %v, want 12.345s", got)
	}
	// A media element reports 0 for a stream whose length it does not know, and
	// the client already converts NaN/Infinity to 0 before sending.
	if got := seconds(0); got != 0 {
		t.Errorf("seconds(0) = %v, want 0", got)
	}
}
