// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	sduiv1 "github.com/mosaic-media/contracts/gen/mosaic/sdui/v1"
	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"
)

// A client that declares nothing gets what every client got before the field
// existed. This is the compatibility guarantee and it is checked by identity,
// not by equality: an undeclared session must not even copy the tree.
func TestUndeclaredVocabularyIsUntouched(t *testing.T) {
	node := ui.Screen(ui.Text(ui.Prop("text", "hello"))).Build()
	out, d := degrade(node, clientVocabulary{})
	if out != node {
		t.Error("an undeclared client had its tree rebuilt; it should be passed through")
	}
	if !d.empty() {
		t.Error("an undeclared client had something degraded")
	}
}

// declaring lists everything the contract has except what is named, so a test
// says what a client is *missing* rather than restating the whole vocabulary.
func declaring(missingPrimitives, missingActions []string) clientVocabulary {
	v := clientVocabulary{
		version:    sdui.VocabularyVersion,
		primitives: map[string]bool{},
		actions:    map[string]bool{},
		declared:   true,
	}
	skip := func(list []string, s string) bool {
		for _, x := range list {
			if x == s {
				return true
			}
		}
		return false
	}
	for _, p := range sdui.Primitives {
		if !skip(missingPrimitives, p.Type) {
			v.primitives[p.Type] = true
		}
	}
	for _, a := range sdui.ActionKinds {
		if !skip(missingActions, a.Kind) {
			v.actions[a.Kind] = true
		}
	}
	return v
}

// degradedTree runs the pass and returns just the tree, for assertions about
// shape rather than about what was reported.
func degradedTree(t *testing.T, node *sduiv1.UINode, v clientVocabulary) *sduiv1.UINode {
	t.Helper()
	out, _ := degrade(node, v)
	return out
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

func types(node *sduiv1.UINode) []string {
	var out []string
	var walk func(*sduiv1.UINode)
	walk = func(n *sduiv1.UINode) {
		if n == nil {
			return
		}
		out = append(out, n.GetType())
		for _, c := range n.GetChildren() {
			walk(c)
		}
		for _, l := range n.GetSlots() {
			for _, c := range l.GetNodes() {
				walk(c)
			}
		}
	}
	walk(node)
	return out
}

func TestAnUnsupportedPrimitiveIsDroppedWholeAndReported(t *testing.T) {
	node := ui.Screen(
		ui.Text(ui.Prop("text", "kept")),
		// A ProgressBar with an Icon inside it: the whole subtree goes, not just
		// the node — leaving the child behind would rearrange the layout rather
		// than simplify it.
		ui.ProgressBar(ui.Icon(ui.Prop("name", "info"))),
	).Build()

	got := types(degradedTree(t, node, declaring([]string{"ProgressBar"}, nil)))
	if contains(got, sdui.TypeProgressBar) {
		t.Errorf("ProgressBar survived: %v", got)
	}
	if contains(got, sdui.TypeIcon) {
		t.Errorf("a child of the dropped node survived: %v", got)
	}
	if !contains(got, sdui.TypeText) {
		t.Errorf("the supported sibling was dropped too: %v", got)
	}
	_, d := degrade(node, declaring([]string{"ProgressBar"}, nil))
	if d.types["ProgressBar"] != 1 {
		t.Errorf("degradation not reported: %+v", d.types)
	}
}

func TestAnUnsupportedPrimitiveInASlotIsDropped(t *testing.T) {
	node := ui.Screen(ui.Slot("aside", ui.Slider(), ui.Text(ui.Prop("text", "x")))).Build()
	out, d := degrade(node, declaring([]string{"Slider"}, nil))
	if contains(types(out), sdui.TypeSlider) {
		t.Error("Slider survived in a slot")
	}
	if d.types["Slider"] != 1 {
		t.Errorf("slot degradation not reported: %+v", d.types)
	}
	if len(out.GetSlots()["aside"].GetNodes()) != 1 {
		t.Error("the supported sibling in the slot was dropped too")
	}
}

// A component is never filtered: the client renders whatever definition it is
// served, so a type outside the primitive tier is none of this pass's business.
// Getting this wrong would blank every screen for any client that declared a
// vocabulary, which is the failure worth a test of its own.
func TestComponentsAndModuleTypesAreNeverDropped(t *testing.T) {
	node := ui.Screen(
		ui.PosterCard("A film", "movie"),
		ui.Component("stremio:AddonRow"),
	).Build()
	// Declare a client missing every primitive there is.
	all := make([]string, 0, len(sdui.Primitives))
	for _, p := range sdui.Primitives {
		all = append(all, p.Type)
	}
	out, d := degrade(node, declaring(all, nil))
	got := types(out)
	if !contains(got, sdui.TypePosterCard) || !contains(got, "stremio:AddonRow") {
		t.Errorf("a component or module type was dropped: %v", got)
	}
	if !d.empty() {
		t.Errorf("nothing should have been degraded: %+v", d)
	}
}

func TestAnUninterpretableActionIsStrippedFromItsNode(t *testing.T) {
	node := ui.Screen(ui.Pressable(ui.OnTap(ui.SetValue("q", "x")))).Build()
	out, d := degrade(node, declaring(nil, []string{sdui.KindSetValue}))
	press := out.GetChildren()[0]
	if press.GetProps().GetFields()["action"] != nil {
		t.Error("an action the client cannot interpret was still sent")
	}
	// The control itself stays. A missing affordance is a bigger change to a
	// screen than an inert one, and the server does not get to make it.
	if press.GetType() != sdui.TypePressable {
		t.Errorf("the control carrying the action was removed: %s", press.GetType())
	}
	if d.actions[sdui.KindSetValue] != 1 {
		t.Errorf("stripped action not reported: %+v", d.actions)
	}
}

// A sequence is all-or-nothing. Half a sequence is a change nobody asked for,
// which is worse than none of it.
func TestASequenceIsStrippedWhenAnyStepIsUninterpretable(t *testing.T) {
	node := ui.Screen(ui.Pressable(ui.OnTap(ui.Sequence(ui.Back(), ui.Submit(ui.Invoke("createLocalUser", nil), ""))))).Build()
	out, d := degrade(node, declaring(nil, []string{sdui.KindSubmit}))
	if out.GetChildren()[0].GetProps().GetFields()["action"] != nil {
		t.Error("a sequence containing an uninterpretable step was still sent")
	}
	if d.actions[sdui.KindSubmit] != 1 {
		t.Errorf("nested action not reported: %+v", d.actions)
	}
}

// A props value that merely has a `kind` field is not an action. The check is
// against the contract's kind set, so screen params carrying their own `kind`
// survive — a false positive here would silently delete real data.
func TestAPropThatIsNotAnActionIsLeftAlone(t *testing.T) {
	node := ui.Screen(ui.Component("Box", ui.Prop("filter", map[string]any{"kind": "series", "year": "1999"}))).Build()
	out, d := degrade(node, declaring(nil, []string{sdui.KindSetValue}))
	if out.GetChildren()[0].GetProps().GetFields()["filter"] == nil {
		t.Error("a non-action prop with a kind field was stripped")
	}
	if !d.empty() {
		t.Errorf("nothing should have been degraded: %+v", d)
	}
}

func TestMissingReportsWhatTheClientLacks(t *testing.T) {
	// Named against the live vocabulary rather than a memorised gap: `Form` used
	// to stand here and stopped being a primitive when it turned out to be a
	// composition, which made this assertion pass by naming nothing.
	p, a := declaring([]string{sdui.TypeSkeleton}, []string{sdui.KindQuery}).missing()
	if len(p) != 1 || p[0] != sdui.TypeSkeleton {
		t.Errorf("missing primitives = %v", p)
	}
	if len(a) != 1 || a[0] != sdui.KindQuery {
		t.Errorf("missing actions = %v", a)
	}
	// An undeclared client is not "missing" anything — it made no claim.
	if p, a := (clientVocabulary{}).missing(); p != nil || a != nil {
		t.Errorf("an undeclared client reported gaps: %v %v", p, a)
	}
}

// --- the definition library ---

func TestDefinitionLibraryUsesAFallbackWhenTheClientLacksAPrimitive(t *testing.T) {
	library := []byte(`[
	  {"name":"Rich","template":{"type":"Box","children":[{"type":"ProgressBar"}]},
	   "fallback":{"type":"Box","children":[{"type":"Text"}]}},
	  {"name":"Plain","template":{"type":"Box","children":[{"type":"Text"}]}}
	]`)

	out := definitionsFor(context.Background(), declaring([]string{"ProgressBar"}, nil), library, "s1")
	var defs []map[string]any
	if err := json.Unmarshal(out, &defs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(defs) != 2 {
		t.Fatalf("definitions dropped: %d", len(defs))
	}
	rich := defs[0]
	if _, ok := rich["fallback"]; ok {
		t.Error("the fallback was sent alongside the template; the client must never see both")
	}
	tmpl, _ := json.Marshal(rich["template"])
	if strings.Contains(string(tmpl), "ProgressBar") {
		t.Errorf("the fallback was not swapped in: %s", tmpl)
	}
	if !strings.Contains(string(tmpl), "Text") {
		t.Errorf("the swapped-in template is not the fallback: %s", tmpl)
	}
}

func TestDefinitionLibraryIsUntouchedForAnUndeclaredClient(t *testing.T) {
	library := []byte(`[{"name":"Rich","template":{"type":"ProgressBar"},"fallback":{"type":"Text"}}]`)
	out := definitionsFor(context.Background(), clientVocabulary{}, library, "s1")
	if string(out) != string(library) {
		t.Errorf("an undeclared client got a filtered library:\n%s", out)
	}
}

// A definition needing a primitive the client lacks, with no fallback, is served
// unchanged rather than omitted: an omitted definition renders as an Unknown
// placeholder everywhere it is used, which is worse than a template with a hole.
func TestADefinitionWithNoFallbackIsStillServed(t *testing.T) {
	library := []byte(`[{"name":"Rich","template":{"type":"Box","children":[{"type":"ProgressBar"}]}}]`)
	out := definitionsFor(context.Background(), declaring([]string{"ProgressBar"}, nil), library, "s1")
	var defs []map[string]any
	if err := json.Unmarshal(out, &defs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(defs) != 1 || defs[0]["name"] != "Rich" {
		t.Fatalf("the definition was dropped: %s", out)
	}
}

// The shipped library must survive filtering by a client that implements the
// whole contract — the case every real client is in today. A filter that
// mangled it would empty every screen.
func TestTheShippedLibrarySurvivesAFullyCapableClient(t *testing.T) {
	out := definitionsFor(context.Background(), declaring(nil, nil), definitionsLibrary, "s1")
	var before, after []map[string]any
	if err := json.Unmarshal(definitionsLibrary, &before); err != nil {
		t.Fatalf("decode shipped: %v", err)
	}
	if err := json.Unmarshal(out, &after); err != nil {
		t.Fatalf("decode filtered: %v", err)
	}
	if len(before) != len(after) {
		t.Fatalf("filtering changed the library size: %d -> %d", len(before), len(after))
	}
	for i := range before {
		b, _ := json.Marshal(before[i]["template"])
		a, _ := json.Marshal(after[i]["template"])
		if string(b) != string(a) {
			t.Errorf("%v: template changed for a fully capable client", before[i]["name"])
		}
	}
}

// The declaration reaches the session from the wire message, which is the part
// a hand-rolled conversion gets wrong.
func TestVocabularyFromTheWire(t *testing.T) {
	v := vocabularyFrom(&sessionv1.VocabularyProfile{
		Version:    "1.0.0",
		Primitives: []string{"Box", "Text"},
		Actions:    []string{"navigate"},
	})
	if !v.declared || v.version != "1.0.0" {
		t.Fatalf("not declared: %+v", v)
	}
	if !v.rendersType("Box") || v.rendersType("Slider") {
		t.Error("primitive support read wrongly off the wire")
	}
	if !v.interpretsAction("navigate") || v.interpretsAction("toast") {
		t.Error("action support read wrongly off the wire")
	}
	if v := vocabularyFrom(nil); v.declared {
		t.Error("a nil profile read as a declaration")
	}
}
