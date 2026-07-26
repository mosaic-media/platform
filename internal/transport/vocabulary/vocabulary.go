// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

// Package vocabulary is ADR 0084's negotiation, and the definition library as
// one particular client should receive it.
//
// It lived inside the session transport until the pre-session bootstrap
// (ADR 0101) needed the same machinery: the doorway carries the same vocabulary
// declaration Attach carries, so negotiation applies to it unchanged, and a
// second copy of "what can this client draw" is exactly the drift the
// declaration exists to remove. Both transports read this one.
package vocabulary

import (
	"context"
	"encoding/json"
	"sort"
	"strings"

	"google.golang.org/protobuf/types/known/structpb"

	sduiv1 "github.com/mosaic-media/contracts/gen/mosaic/sdui/v1"
	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// What a client can *render*, and what the Platform does about what it cannot.
//
// The declaration is the other half of the question ClientProfile answers: that
// one is about decoding bytes, this one about drawing nodes. Both ride Attach,
// both are optional, and both treat an absent declaration as the assumption made
// before the field existed — here, that the client implements the whole
// vocabulary, which is what every client was assumed to do.
//
// Degradation is deliberate rather than incidental. Before this, an unsupported
// node reached the client and became a labelled placeholder: forward-compatible,
// and completely silent. Nobody learned that a screen had a hole in it unless
// they were looking at that screen. So the server now declines to emit what the
// client said it cannot draw, and *says so* — a telemetry event naming the type
// and the screen is the whole difference between degrading and failing quietly.
//
// Only the primitive tier is filtered. Components are definitions the server
// delivers (ADR 0040), so a client renders whatever it is sent; the thing it can
// fail to draw is a primitive inside a template, which is what the definition
// library's per-session fallback selection answers.

// contractPrimitives is the primitive tier as the contract publishes it
// (ADR 0083). Reading it rather than keeping a list here is the point: a
// primitive added to the vocabulary is one this filter knows about without
// anyone remembering to add it.
var contractPrimitives = func() map[string]bool {
	m := make(map[string]bool, len(sdui.Primitives))
	for _, p := range sdui.Primitives {
		m[p.Type] = true
	}
	return m
}()

// contractActionKinds is the same for the action tier: what counts as an action
// envelope at all. A props value carrying some other `kind` is somebody else's
// data and is left alone.
var contractActionKinds = func() map[string]bool {
	m := make(map[string]bool, len(sdui.ActionKinds))
	for _, a := range sdui.ActionKinds {
		m[a.Kind] = true
	}
	return m
}()

// Client is what a client declared it can render.
type Client struct {
	// version is the vocabulary edition the client was built against. It is not
	// a claim to implement all of it — primitives and actions are the precise
	// answer — so nothing branches on it yet; it is recorded so a version skew
	// is visible in telemetry before it is visible as a broken screen.
	version    string
	primitives map[string]bool
	actions    map[string]bool
	// declared distinguishes "this client renders nothing" from "this client
	// said nothing at all", exactly as clientProfile does. The first is a real
	// answer to respect; the second is an older client and gets everything.
	declared bool
}

// From converts a declaration into the filter the emit side applies.
func From(p *sessionv1.VocabularyProfile) Client {
	if p == nil {
		return Client{}
	}
	v := Client{
		version:    p.GetVersion(),
		primitives: make(map[string]bool, len(p.GetPrimitives())),
		actions:    make(map[string]bool, len(p.GetActions())),
		declared:   true,
	}
	for _, t := range p.GetPrimitives() {
		v.primitives[t] = true
	}
	for _, k := range p.GetActions() {
		v.actions[k] = true
	}
	return v
}

// Version is the vocabulary edition the client was built against, empty when it
// did not say. Nothing branches on it; it is reported so a version skew is
// visible in telemetry before it is visible as a broken screen.
func (v Client) Version() string { return v.version }

// Counts is how many primitives and action kinds the client declared, for the
// one telemetry line the declaration earns.
func (v Client) Counts() (primitives, actions int) {
	return len(v.primitives), len(v.actions)
}

// Declared reports whether the client said anything at all. An undeclared
// client is an older one and gets everything, which is not the same answer as a
// client that declared nothing.
func (v Client) Declared() bool { return v.declared }

// RendersType reports whether the client can draw a node of this type. Anything
// that is not a primitive — a component, a module's own type — is the client's
// to render from a definition, so it is always allowed through.
func (v Client) RendersType(typ string) bool {
	if !v.declared || !contractPrimitives[typ] {
		return true
	}
	return v.primitives[typ]
}

// InterpretsAction reports whether the client's dispatcher handles this kind.
func (v Client) InterpretsAction(kind string) bool {
	if !v.declared {
		return true
	}
	return v.actions[kind]
}

// Missing lists what the contract declares and this client did not, for the one
// telemetry line written when the declaration arrives. It is the number that
// says whether a client is behind the contract, which nothing could say before.
func (v Client) Missing() (primitives, actions []string) {
	if !v.declared {
		return nil, nil
	}
	for _, p := range sdui.Primitives {
		if !v.primitives[p.Type] {
			primitives = append(primitives, p.Type)
		}
	}
	for _, a := range sdui.ActionKinds {
		if !v.actions[a.Kind] {
			actions = append(actions, a.Kind)
		}
	}
	sort.Strings(primitives)
	sort.Strings(actions)
	return primitives, actions
}

// Degradation records what a pass removed, so it can be reported once per push
// rather than once per node — a screen full of PosterCards on a client with no
// ProgressBar would otherwise write one line per card.
type Degradation struct {
	types   map[string]int
	actions map[string]int
}

func (d *Degradation) dropType(t string) {
	if d.types == nil {
		d.types = map[string]int{}
	}
	d.types[t]++
}

func (d *Degradation) dropAction(k string) {
	if d.actions == nil {
		d.actions = map[string]int{}
	}
	d.actions[k]++
}

func (d *Degradation) empty() bool { return len(d.types) == 0 && len(d.actions) == 0 }

// summary renders the counts as a stable string ("PosterCard×4"), sorted so the
// same Degradation reads the same way in every log line.
func summary(counts map[string]int) string {
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"×"+itoa(counts[k]))
	}
	return strings.Join(parts, ", ")
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// Degrade returns node with everything the client cannot render removed, plus a
// record of what went. It returns the node unchanged — and allocates nothing —
// for an undeclared client, which is every client built before this field.
//
// A dropped subtree is dropped whole: a node's children are its own, and keeping
// orphaned grandchildren in the parent's place would rearrange a layout rather
// than simplify it.
func Degrade(node *sduiv1.UINode, v Client) (*sduiv1.UINode, Degradation) {
	var d Degradation
	if !v.declared || node == nil {
		return node, d
	}
	return degradeNode(node, v, &d), d
}

func degradeNode(node *sduiv1.UINode, v Client, d *Degradation) *sduiv1.UINode {
	if node == nil {
		return nil
	}
	out := &sduiv1.UINode{Type: node.GetType(), Id: node.GetId()}
	if p := node.GetProps(); p != nil {
		out.Props = degradeProps(p, v, d)
	}
	for _, c := range node.GetChildren() {
		if !v.RendersType(c.GetType()) {
			d.dropType(c.GetType())
			continue
		}
		out.Children = append(out.Children, degradeNode(c, v, d))
	}
	if len(node.GetSlots()) > 0 {
		out.Slots = make(map[string]*sduiv1.NodeList, len(node.GetSlots()))
		for name, list := range node.GetSlots() {
			kept := &sduiv1.NodeList{}
			for _, c := range list.GetNodes() {
				if !v.RendersType(c.GetType()) {
					d.dropType(c.GetType())
					continue
				}
				kept.Nodes = append(kept.Nodes, degradeNode(c, v, d))
			}
			out.Slots[name] = kept
		}
	}
	return out
}

// degradeProps strips props whose value is an action envelope of a kind this
// client does not interpret.
//
// The prop is removed rather than the control that carries it. A button with no
// action is inert and visible; removing the button removes an affordance the
// server decided the screen needed, and the client may well have its own
// disabled rendering for a control with nothing wired to it. Either way it is
// reported, which is the part that was Missing.
func degradeProps(props *structpb.Struct, v Client, d *Degradation) *structpb.Struct {
	changed := false
	fields := make(map[string]*structpb.Value, len(props.GetFields()))
	for k, val := range props.GetFields() {
		if kind, ok := unsupportedAction(val, v); ok {
			d.dropAction(kind)
			changed = true
			continue
		}
		fields[k] = val
	}
	if !changed {
		return props
	}
	return &structpb.Struct{Fields: fields}
}

// unsupportedAction reports whether a props value is an action envelope of a
// kind the client does not interpret, and which kind that is.
//
// "Is this an action?" is answered against the contract's own kind set rather
// than by the presence of a `kind` key, so a screen param that happens to carry
// a field called kind is not mistaken for one. A nested sequence disqualifies
// the whole envelope: a sequence that ran half its steps is a worse outcome than
// one that does not run, because the half it ran is a change nobody asked for.
func unsupportedAction(val *structpb.Value, v Client) (string, bool) {
	s := val.GetStructValue()
	if s == nil {
		return "", false
	}
	kind := s.GetFields()["kind"].GetStringValue()
	if kind == "" || !contractActionKinds[kind] {
		return "", false
	}
	if !v.InterpretsAction(kind) {
		return kind, true
	}
	for _, nested := range s.GetFields()["actions"].GetListValue().GetValues() {
		if k, bad := unsupportedAction(nested, v); bad {
			return k, true
		}
	}
	return "", false
}

// Report writes the one telemetry line a degraded push earns. This is the whole
// point of the slice: the client would have rendered a placeholder either way,
// and nothing would have known.
func (d Degradation) Report(ctx context.Context, session, where string) {
	if d.empty() {
		return
	}
	fields := []telemetry.Field{
		telemetry.Identifier("session", session),
		telemetry.String("where", where),
	}
	if len(d.types) > 0 {
		fields = append(fields, telemetry.String("types", summary(d.types)))
	}
	if len(d.actions) > 0 {
		fields = append(fields, telemetry.String("actions", summary(d.actions)))
	}
	telemetry.From(ctx).Warn("sdui degraded for this client's declared vocabulary", fields...)
}

// DefinitionsFor returns the component library as this client should receive it:
// a definition whose template needs a primitive the client did not declare is
// served with its fallback instead, when it has one.
//
// The selection is the server's because only the server knows the client's
// declaration at the moment it sends the library, and because sending both
// templates would make every client carry the cost of a Degradation only some of
// them need.
//
// A definition that needs a Missing primitive and has no fallback is served
// unchanged and reported. There is nothing better to send — an omitted
// definition renders as an Unknown placeholder for every node that uses it,
// which is strictly worse than a template with a hole in it — but silence is not
// the answer either.
func DefinitionsFor(ctx context.Context, v Client, library []byte, session string) []byte {
	if !v.declared {
		return library
	}
	var defs []map[string]json.RawMessage
	if err := json.Unmarshal(library, &defs); err != nil {
		// The library is an embedded build artefact; a failure here is not a
		// runtime condition. Serve it unchanged rather than serve nothing.
		telemetry.From(ctx).Error("sdui definition library is undecodable; serving it unfiltered", telemetry.Err(err))
		return library
	}

	swapped, unfixable := []string{}, []string{}
	for _, def := range defs {
		name := jsonString(def["name"])
		need := templatePrimitives(def["template"])
		if supportsAll(v, need) {
			delete(def, "fallback")
			continue
		}
		if fb, ok := def["fallback"]; ok && len(fb) > 0 && string(fb) != "null" {
			def["template"] = fb
			delete(def, "fallback")
			swapped = append(swapped, name)
			continue
		}
		delete(def, "fallback")
		unfixable = append(unfixable, name)
	}

	out, err := json.Marshal(defs)
	if err != nil {
		return library
	}
	if len(swapped) > 0 || len(unfixable) > 0 {
		sort.Strings(swapped)
		sort.Strings(unfixable)
		telemetry.From(ctx).Info("sdui definition library filtered for this client",
			telemetry.Identifier("session", session),
			telemetry.String("fallbacks_used", strings.Join(swapped, ",")),
			telemetry.String("no_fallback", strings.Join(unfixable, ",")))
	}
	return out
}

func supportsAll(v Client, need []string) bool {
	for _, t := range need {
		if !v.RendersType(t) {
			return false
		}
	}
	return true
}

// templatePrimitives lists the primitive types a template expands into.
func templatePrimitives(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var tree any
	if err := json.Unmarshal(raw, &tree); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var walk func(any)
	walk = func(v any) {
		switch x := v.(type) {
		case map[string]any:
			if t, ok := x["type"].(string); ok && contractPrimitives[t] {
				seen[t] = true
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
	out := make([]string, 0, len(seen))
	for t := range seen {
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

func jsonString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}
