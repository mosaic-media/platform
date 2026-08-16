// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package vocabulary

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

// TestUndeclaredVocabularyIsUntouched pins the compatibility guarantee: a client
// that declares nothing gets everything. Checked by identity rather than
// equality, because an undeclared session must not even copy the tree.
func TestUndeclaredVocabularyIsUntouched(t *testing.T) {
	node := ui.Screen(ui.Text(ui.Prop("text", "hello"))).Build()
	out, d := Degrade(node, Client{})
	if out != node {
		t.Error("an undeclared client had its tree rebuilt; it should be passed through")
	}
	if !d.empty() {
		t.Error("an undeclared client had something degraded")
	}
}

// declaring lists everything the contract has except what is named, so a test
// says what a client is missing rather than restating the whole vocabulary.
func declaring(missingPrimitives, missingActions []string) Client {
	v := Client{
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
func degradedTree(t *testing.T, node *sduiv1.UINode, v Client) *sduiv1.UINode {
	t.Helper()
	out, _ := Degrade(node, v)
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
	_, d := Degrade(node, declaring([]string{"ProgressBar"}, nil))
	if d.types["ProgressBar"] != 1 {
		t.Errorf("Degradation not reported: %+v", d.types)
	}
}

func TestAnUnsupportedPrimitiveInASlotIsDropped(t *testing.T) {
	node := ui.Screen(ui.Slot("aside", ui.Slider(), ui.Text(ui.Prop("text", "x")))).Build()
	out, d := Degrade(node, declaring([]string{"Slider"}, nil))
	if contains(types(out), sdui.TypeSlider) {
		t.Error("Slider survived in a slot")
	}
	if d.types["Slider"] != 1 {
		t.Errorf("slot Degradation not reported: %+v", d.types)
	}
	if len(out.GetSlots()["aside"].GetNodes()) != 1 {
		t.Error("the supported sibling in the slot was dropped too")
	}
}

// TestComponentsAndModuleTypesAreNeverDropped pins that only the primitive tier
// is filtered: the client renders whatever definition it is served, so a type
// outside that tier is none of this pass's business. Getting it wrong blanks
// every screen for any client that declared a vocabulary.
func TestComponentsAndModuleTypesAreNeverDropped(t *testing.T) {
	node := ui.Screen(
		ui.PosterCard("A film", "movie"),
		ui.Component("stremio:AddonRow"),
	).Build()
	// Declare a client Missing every primitive there is.
	all := make([]string, 0, len(sdui.Primitives))
	for _, p := range sdui.Primitives {
		all = append(all, p.Type)
	}
	out, d := Degrade(node, declaring(all, nil))
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
	out, d := Degrade(node, declaring(nil, []string{sdui.KindSetValue}))
	press := out.GetChildren()[0]
	if press.GetProps().GetFields()["action"] != nil {
		t.Error("an action the client cannot interpret was still sent")
	}
	// The control itself stays. A Missing affordance is a bigger change to a
	// screen than an inert one, and the server does not get to make it.
	if press.GetType() != sdui.TypePressable {
		t.Errorf("the control carrying the action was removed: %s", press.GetType())
	}
	if d.actions[sdui.KindSetValue] != 1 {
		t.Errorf("stripped action not reported: %+v", d.actions)
	}
}

// TestASequenceIsStrippedWhenAnyStepIsUninterpretable pins that a sequence is
// all-or-nothing: half a sequence is a change nobody asked for, which is worse
// than none of it.
func TestASequenceIsStrippedWhenAnyStepIsUninterpretable(t *testing.T) {
	node := ui.Screen(ui.Pressable(ui.OnTap(ui.Sequence(ui.Back(), ui.Submit(ui.Invoke("createLocalUser", nil), ""))))).Build()
	out, d := Degrade(node, declaring(nil, []string{sdui.KindSubmit}))
	if out.GetChildren()[0].GetProps().GetFields()["action"] != nil {
		t.Error("a sequence containing an uninterpretable step was still sent")
	}
	if d.actions[sdui.KindSubmit] != 1 {
		t.Errorf("nested action not reported: %+v", d.actions)
	}
}

// TestAPropThatIsNotAnActionIsLeftAlone pins that a props value which merely has
// a kind field is not an action. The check is against the contract's kind set,
// so screen params carrying their own kind survive; a false positive here
// silently deletes real data.
func TestAPropThatIsNotAnActionIsLeftAlone(t *testing.T) {
	node := ui.Screen(ui.Component("Box", ui.Prop("filter", map[string]any{"kind": "series", "year": "1999"}))).Build()
	out, d := Degrade(node, declaring(nil, []string{sdui.KindSetValue}))
	if out.GetChildren()[0].GetProps().GetFields()["filter"] == nil {
		t.Error("a non-action prop with a kind field was stripped")
	}
	if !d.empty() {
		t.Errorf("nothing should have been degraded: %+v", d)
	}
}

func TestMissingReportsWhatTheClientLacks(t *testing.T) {
	// Named against the live vocabulary rather than a memorised gap: a type that
	// stops being a primitive makes this assertion pass by naming nothing.
	p, a := declaring([]string{sdui.TypeSkeleton}, []string{sdui.KindQuery}).Missing()
	if len(p) != 1 || p[0] != sdui.TypeSkeleton {
		t.Errorf("Missing primitives = %v", p)
	}
	if len(a) != 1 || a[0] != sdui.KindQuery {
		t.Errorf("Missing actions = %v", a)
	}
	// An undeclared client is not "Missing" anything — it made no claim.
	if p, a := (Client{}).Missing(); p != nil || a != nil {
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

	out := DefinitionsFor(context.Background(), declaring([]string{"ProgressBar"}, nil), library, "s1")
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
	out := DefinitionsFor(context.Background(), Client{}, library, "s1")
	if string(out) != string(library) {
		t.Errorf("an undeclared client got a filtered library:\n%s", out)
	}
}

// TestADefinitionWithNoFallbackIsStillServed pins that a definition needing a
// primitive the client lacks is served unchanged rather than omitted: an omitted
// definition renders as an Unknown placeholder everywhere it is used, which is
// worse than a template with a hole.
func TestADefinitionWithNoFallbackIsStillServed(t *testing.T) {
	library := []byte(`[{"name":"Rich","template":{"type":"Box","children":[{"type":"ProgressBar"}]}}]`)
	out := DefinitionsFor(context.Background(), declaring([]string{"ProgressBar"}, nil), library, "s1")
	var defs []map[string]any
	if err := json.Unmarshal(out, &defs); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(defs) != 1 || defs[0]["name"] != "Rich" {
		t.Fatalf("the definition was dropped: %s", out)
	}
}

// TestTheShippedLibrarySurvivesAFullyCapableClient pins that filtering leaves
// the shipped library untouched for a client implementing the whole contract —
// the case every real client is in. A filter that mangled it would empty every
// screen.
func TestTheShippedLibrarySurvivesAFullyCapableClient(t *testing.T) {
	out := DefinitionsFor(context.Background(), declaring(nil, nil), Library(), "s1")
	var before, after []map[string]any
	if err := json.Unmarshal(Library(), &before); err != nil {
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

// TestVocabularyFromTheWire pins the conversion from the wire message into the
// declaration the emit side reads.
func TestVocabularyFromTheWire(t *testing.T) {
	v := From(&sessionv1.VocabularyProfile{
		Version:    "1.0.0",
		Primitives: []string{"Box", "Text"},
		Actions:    []string{"navigate"},
	})
	if !v.declared || v.version != "1.0.0" {
		t.Fatalf("not declared: %+v", v)
	}
	if !v.RendersType("Box") || v.RendersType("Slider") {
		t.Error("primitive support read wrongly off the wire")
	}
	if !v.InterpretsAction("navigate") || v.InterpretsAction("toast") {
		t.Error("action support read wrongly off the wire")
	}
	if v := From(nil); v.declared {
		t.Error("a nil profile read as a declaration")
	}
}
