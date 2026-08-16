// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package vocabulary

import (
	"encoding/json"
	"sort"

	sduiv1 "github.com/mosaic-media/contracts/gen/mosaic/sdui/v1"
	"google.golang.org/protobuf/types/known/structpb"
)

// Subset returns the definitions a tree needs and no more, transitively closed
// (platform#57).
//
// The subset is a security property, not an optimisation. The bootstrap response
// is the one payload an unauthenticated party can enumerate, so it must describe
// a doorway and nothing else. Do not replace it with the whole library because
// that is shorter and always works; the test beside it asserts the result is
// strictly smaller than what it was given.
//
// Transitively, because a definition's template may name another component:
// ExtensionCard expands into a Badge, RelatedRail into a Carousel. A one-level
// pass serves a tree whose components resolve and whose components' components
// do not, which renders as an Unknown placeholder in the middle of an otherwise
// correct screen.
//
// A type the library does not define is not an error: it is a primitive, or a
// module's own namespaced type, and neither has a definition to send.
func Subset(library []byte, tree *sduiv1.UINode) ([]byte, error) {
	var defs []map[string]json.RawMessage
	if err := json.Unmarshal(library, &defs); err != nil {
		return nil, err
	}

	byName := make(map[string]map[string]json.RawMessage, len(defs))
	order := make([]string, 0, len(defs))
	for _, def := range defs {
		name := jsonString(def["name"])
		if name == "" {
			continue
		}
		byName[name] = def
		order = append(order, name)
	}

	// Breadth-first over the closure. Iterative rather than recursive because
	// the input is data: a definition library with a reference cycle in it would
	// blow the stack, and seen makes a cycle terminate instead.
	need := map[string]bool{}
	queue := nodeTypes(tree)
	for len(queue) > 0 {
		name := queue[0]
		queue = queue[1:]
		if need[name] {
			continue
		}
		def, ok := byName[name]
		if !ok {
			// A primitive, or a type nothing here defines. Nothing to send.
			continue
		}
		need[name] = true
		queue = append(queue, templateComponentTypes(def["template"], byName)...)
		// The fallback counts too. It is what this definition becomes for a
		// client missing a primitive, so a component it names must travel with
		// it or the degradation would be the thing that broke the screen.
		queue = append(queue, templateComponentTypes(def["fallback"], byName)...)
	}

	// Emitted in the library's own order, so two subsets of the same tree are
	// byte-identical and a diff of what the Platform served is readable.
	out := make([]map[string]json.RawMessage, 0, len(need))
	for _, name := range order {
		if need[name] {
			out = append(out, byName[name])
		}
	}
	return json.Marshal(out)
}

// SubsetNames is Subset's answer as a sorted list of names, for a test or a log
// line that wants to say what was served rather than how many bytes it was.
func SubsetNames(library []byte, tree *sduiv1.UINode) ([]string, error) {
	raw, err := Subset(library, tree)
	if err != nil {
		return nil, err
	}
	var defs []map[string]json.RawMessage
	if err := json.Unmarshal(raw, &defs); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, jsonString(def["name"]))
	}
	sort.Strings(names)
	return names, nil
}

// nodeTypes lists every type named anywhere in a tree — children, slots, and
// the props of both. A component can be carried in a prop rather than as a
// child (an overlay's body, a definition's row template), and one missed there
// is a placeholder in the middle of a screen.
func nodeTypes(node *sduiv1.UINode) []string {
	var out []string
	var walk func(*sduiv1.UINode)
	walk = func(n *sduiv1.UINode) {
		if n == nil {
			return
		}
		if t := n.GetType(); t != "" {
			out = append(out, t)
		}
		out = append(out, structTypes(n.GetProps())...)
		for _, c := range n.GetChildren() {
			walk(c)
		}
		for _, list := range n.GetSlots() {
			for _, c := range list.GetNodes() {
				walk(c)
			}
		}
	}
	walk(node)
	return out
}

// structTypes finds node-shaped values inside a props bag: any object carrying a
// string type. It is deliberately generous — a false positive is a name the
// library does not define, which costs nothing, while a false negative is a
// missing definition.
func structTypes(s *structpb.Struct) []string {
	var out []string
	var walkValue func(*structpb.Value)
	walkStruct := func(st *structpb.Struct) {
		if t := st.GetFields()["type"].GetStringValue(); t != "" {
			out = append(out, t)
		}
		for _, v := range st.GetFields() {
			walkValue(v)
		}
	}
	walkValue = func(v *structpb.Value) {
		switch {
		case v.GetStructValue() != nil:
			walkStruct(v.GetStructValue())
		case v.GetListValue() != nil:
			for _, item := range v.GetListValue().GetValues() {
				walkValue(item)
			}
		}
	}
	if s == nil {
		return nil
	}
	walkStruct(s)
	return out
}

// templateComponentTypes lists the component types a template expands into —
// the ones the library defines, so a primitive falls out here rather than being
// queued and dropped later.
func templateComponentTypes(raw json.RawMessage, byName map[string]map[string]json.RawMessage) []string {
	if len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if t, ok := x["type"].(string); ok {
				if _, defined := byName[t]; defined {
					out = append(out, t)
				}
			}
			for _, val := range x {
				walk(val)
			}
		case []any:
			for _, val := range x {
				walk(val)
			}
		}
	}
	walk(tree)
	return out
}
