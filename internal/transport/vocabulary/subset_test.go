// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package vocabulary_test

import (
	"encoding/json"
	"testing"

	"github.com/mosaic-media/contracts/ui"
	"github.com/mosaic-media/platform/internal/transport/vocabulary"
)

// The subset is the security property of the one payload an unauthenticated
// party can enumerate (platform#57), and the pressure on it will always be to send
// the library instead — one line shorter, and it always works. These are what
// say it did not.

func TestSubsetIsStrictlySmallerThanTheLibrary(t *testing.T) {
	tree := ui.Screen(ui.Title("Mosaic"), ui.Section("Sign in", ui.Banner("hello", ui.ToneInfo))).Build()

	names, err := vocabulary.SubsetNames(vocabulary.Library(), tree)
	if err != nil {
		t.Fatalf("SubsetNames: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("the subset is empty; the doorway would render as placeholders")
	}

	var whole []json.RawMessage
	if err := json.Unmarshal(vocabulary.Library(), &whole); err != nil {
		t.Fatalf("decode library: %v", err)
	}
	if len(names) >= len(whole) {
		t.Fatalf("the subset is %d of %d definitions — it is the library, which is the failure this exists to prevent",
			len(names), len(whole))
	}

	// Precisely what the doorway needs, and nothing beside it: a definition that
	// crept in would be a component shape disclosed to an unauthenticated
	// caller for no reason.
	want := map[string]bool{"Screen": true, "Section": true, "Banner": true}
	for _, n := range names {
		if !want[n] {
			t.Errorf("the subset carries %q, which nothing in the tree needs", n)
		}
		delete(want, n)
	}
	for n := range want {
		t.Errorf("the subset is missing %q, which the tree names", n)
	}
}

// The closure is transitive because definition templates name other
// components: ExtensionCard expands into a Badge, RelatedRail into a Carousel.
// A one-level pass serves a tree whose components resolve and whose components'
// components do not, which draws an Unknown placeholder inside an otherwise
// correct screen.
func TestSubsetFollowsComponentsNamedByTemplates(t *testing.T) {
	tree := ui.ExtensionCard("Something", ui.Summary("a module")).Build()

	names, err := vocabulary.SubsetNames(vocabulary.Library(), tree)
	if err != nil {
		t.Fatalf("SubsetNames: %v", err)
	}
	has := func(n string) bool {
		for _, got := range names {
			if got == n {
				return true
			}
		}
		return false
	}
	if !has("ExtensionCard") {
		t.Fatalf("the subset is missing the component the tree names: %v", names)
	}
	if !has("Badge") {
		t.Fatalf("the subset stopped at one level: ExtensionCard expands into a Badge, and got %v", names)
	}
}

// A component can be carried in a prop rather than as a child — an overlay's
// body is the case in the shipped surface — and one missed there is a
// placeholder in the middle of a screen.
func TestSubsetFindsComponentsCarriedInProps(t *testing.T) {
	tree := ui.Button("Install…", "quiet",
		ui.OnTap(ui.Overlay(ui.SurfaceModal, ui.ExtensionCard("Something")))).Build()

	names, err := vocabulary.SubsetNames(vocabulary.Library(), tree)
	if err != nil {
		t.Fatalf("SubsetNames: %v", err)
	}
	found := map[string]bool{}
	for _, n := range names {
		found[n] = true
	}
	if !found["ExtensionCard"] {
		t.Fatalf("a component carried in a prop was not followed: %v", names)
	}
	if !found["Badge"] {
		t.Fatalf("the closure did not continue through a prop-carried component: %v", names)
	}
}

// A type nothing defines is a primitive or a module's own namespaced type.
// Neither has a definition to send, and neither is an error.
func TestSubsetIgnoresTypesTheLibraryDoesNotDefine(t *testing.T) {
	tree := ui.Component("acme.WeirdThing").Build()
	names, err := vocabulary.SubsetNames(vocabulary.Library(), tree)
	if err != nil {
		t.Fatalf("SubsetNames: %v", err)
	}
	if len(names) != 0 {
		t.Fatalf("subset = %v, want nothing for a type the library does not define", names)
	}
}

// Two subsets of the same tree must be byte-identical, so a diff of what the
// Platform served is readable and a cache key over it is stable.
func TestSubsetIsDeterministic(t *testing.T) {
	tree := ui.Screen(ui.Title("Mosaic"), ui.Section("Sign in", ui.Banner("hello", ui.ToneInfo))).Build()
	first, err := vocabulary.Subset(vocabulary.Library(), tree)
	if err != nil {
		t.Fatalf("Subset: %v", err)
	}
	second, err := vocabulary.Subset(vocabulary.Library(), tree)
	if err != nil {
		t.Fatalf("Subset: %v", err)
	}
	if string(first) != string(second) {
		t.Fatal("two subsets of one tree differ")
	}
}
