// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package app

import (
	"strings"
	"testing"
)

// The namespace rule, at the one boundary a module's own UI crosses (ADR 0085).
//
// These call the unexported validator directly rather than driving the whole
// query, because what is under test is the rule and its message — the query path
// around it is covered by the boundary conformance tests.

func TestAModuleMayComposeItsSettingsFromCoreTypes(t *testing.T) {
	// What every shipped module actually returns: a Screen of core nodes.
	ui := []byte(`{
	  "type":"Screen",
	  "props":{"title":"Stremio addons"},
	  "children":[
	    {"type":"Box","children":[{"type":"Text","props":{"text":"Installed"}}]},
	    {"type":"SubmitField","props":{"placeholder":"Manifest URL"}}
	  ],
	  "slots":{"aside":[{"type":"Badge","props":{"label":"3"}}]}
	}`)
	if err := validateUINode("stremio", ui); err != nil {
		t.Fatalf("a core-only settings screen was refused: %v", err)
	}
}

func TestAModuleMayEmitItsOwnNamespacedType(t *testing.T) {
	ui := []byte(`{"type":"Screen","children":[{"type":"stremio:AddonRow"}]}`)
	if err := validateUINode("stremio", ui); err != nil {
		t.Fatalf("a module's own type was refused: %v", err)
	}
}

// The first live hole. Two modules both contributing `StatChip` overwrite each
// other in the client's registry, last writer winning, with no error anywhere.
func TestAnUnprefixedUnknownTypeIsRefused(t *testing.T) {
	ui := []byte(`{"type":"Screen","children":[{"type":"StatChip"}]}`)
	err := validateUINode("stremio", ui)
	if err == nil {
		t.Fatal("an unprefixed unknown type was accepted")
	}
	if !strings.Contains(err.Error(), "stremio:StatChip") {
		t.Errorf("the error does not say what to write instead: %v", err)
	}
}

// The second. A module naming a core component takes its place on every screen
// in the product, not only its own.
func TestAModuleMayNotShadowACoreComponent(t *testing.T) {
	// Using a PosterCard is composing and is fine.
	if err := validateUINode("stremio", []byte(`{"type":"Screen","children":[{"type":"PosterCard"}]}`)); err != nil {
		t.Fatalf("using a core component was refused: %v", err)
	}
	// Claiming the name is a different act, and it can only be spelled
	// namespaced — which is a different type, and that is the fix.
	if err := validateUINode("stremio", []byte(`{"type":"stremio:PosterCard"}`)); err != nil {
		t.Fatalf("a namespaced name that happens to match a core one was refused: %v", err)
	}
}

func TestAModuleMayNotEmitAnotherModulesType(t *testing.T) {
	ui := []byte(`{"type":"Screen","children":[{"type":"aiostreams:StreamRow"}]}`)
	err := validateUINode("stremio", ui)
	if err == nil {
		t.Fatal("one module emitted another's namespaced type")
	}
	if !strings.Contains(err.Error(), "aiostreams") {
		t.Errorf("the error does not name the owner: %v", err)
	}
}

// A bad type two levels down is the kind that survives being looked at, so the
// whole tree is walked — children and slots alike.
func TestABadTypeDeepInTheTreeIsFound(t *testing.T) {
	deep := []byte(`{"type":"Screen","children":[{"type":"Box","children":[
	  {"type":"Box","children":[{"type":"RogueRow"}]}]}]}`)
	if err := validateUINode("stremio", deep); err == nil {
		t.Error("a bad type three levels down was accepted")
	}
	inSlot := []byte(`{"type":"Screen","slots":{"aside":[{"type":"Box","children":[{"type":"RogueRow"}]}]}}`)
	if err := validateUINode("stremio", inSlot); err == nil {
		t.Error("a bad type inside a slot was accepted")
	}
	// A slot may carry a single node rather than a list.
	single := []byte(`{"type":"Screen","slots":{"aside":{"type":"RogueRow"}}}`)
	if err := validateUINode("stremio", single); err == nil {
		t.Error("a bad type in a single-node slot was accepted")
	}
}

// Props are an open bag. A module's own data carrying a field called "type" is
// data, not a node, and treating it as one would refuse a legitimate screen.
func TestAPropCalledTypeIsNotANode(t *testing.T) {
	ui := []byte(`{"type":"Screen","props":{"filter":{"type":"series","year":"1999"}},
	  "children":[{"type":"Text","props":{"meta":[{"type":"anything at all"}]}}]}`)
	if err := validateUINode("stremio", ui); err != nil {
		t.Fatalf("a prop that happens to be called type was read as a node: %v", err)
	}
}

func TestTheRootIsHeldToTheSameRule(t *testing.T) {
	if err := validateUINode("stremio", []byte(`{"type":"RogueScreen"}`)); err == nil {
		t.Error("a rogue root type was accepted")
	}
	if err := validateUINode("stremio", []byte(`{"props":{}}`)); err == nil {
		t.Error("a root with no type was accepted")
	}
	if err := validateUINode("stremio", nil); err == nil {
		t.Error("an empty settings UI was accepted")
	}
}

// A module id that cannot carry a namespace is refused before its tree is even
// read: `a:b` would make `a:b:Row` ambiguous.
func TestAModuleIDThatCannotBeANamespaceIsRefused(t *testing.T) {
	if err := validateUINode("bad:id", []byte(`{"type":"Screen"}`)); err == nil {
		t.Error("a module id containing the separator was accepted")
	}
}
