// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The measurement platform#39 requires before the callback protocol is fixed.
//
// That record leaves callback chattiness open on purpose: "A tree import makes
// many ContentService calls, each now a round trip. A Unix socket is fast enough
// that this is probably fine, but it should be measured against a real Stremio
// import before the protocol is fixed, and allowed to send the service shape
// back for coarser, batched verbs."
//
// A tree import's cost is (calls per import) × (cost per call). Those are two
// different quantities with two different owners — the first is module
// behaviour, the second is this boundary — and measuring them together would
// produce a number that says nothing about either.
//
// So this measures cost per call, hermetically, by running an import that makes
// a known number of callbacks. The call count for a real import is a property of
// the module and countable from its own tests; multiplying is the reader's job,
// and it keeps this out of the network and the live Stremio addon ecosystem.
//
// The number that matters is the marginal cost of one additional callback, not
// the total: the total includes process spawn and handshake, which happen once
// per module rather than once per import. Comparing 1 child against many is what
// isolates it.

func TestCallbackRoundTripCost(t *testing.T) {
	if testing.Short() {
		t.Skip("timing measurement; skipped under -short")
	}

	m := launch(t, &countingContent{})

	// Two runs whose difference is only the callback count, so process spawn,
	// handshake and the first-call connection setup cancel out.
	const (
		few  = 1
		many = 201
	)

	fewDur := timeImport(t, m, few)
	manyDur := timeImport(t, m, many)

	marginal := (manyDur - fewDur) / time.Duration(many-few)

	t.Logf("callback round-trip cost over the boundary:")
	t.Logf("  %3d callbacks: %v", few, fewDur)
	t.Logf("  %3d callbacks: %v", many, manyDur)
	t.Logf("  marginal cost per callback: %v", marginal)
	t.Logf("  implied cost of a 24-episode season (25 calls): %v", marginal*25)

	// Not an assertion about speed — a regression guard with a ceiling loose
	// enough to survive a loaded CI machine. If a callback ever costs more than
	// this, the batched-verb question platform#39 left open has been answered for
	// us and the protocol needs revisiting rather than the test relaxing.
	const ceiling = 5 * time.Millisecond
	if marginal > ceiling {
		t.Errorf("a callback costs %v, over the %v ceiling — "+
			"platform#39's batched-verb question needs answering", marginal, ceiling)
	}
}

// The same work with no boundary, so the measurement above has something to be
// a multiple of. This is the cost the in-process tier pays.
func BenchmarkInProcessCallback(b *testing.B) {
	svc := &countingContent{}
	ctx := context.Background()
	cmd := v1.AddContentChildCommand{
		Caller:   v1.CallerFromSession("s"),
		ParentID: "w1",
		Kind:     v1.NodeItem,
		Title:    "episode",
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := svc.AddContentChild(ctx, cmd); err != nil {
			b.Fatal(err)
		}
	}
}

func timeImport(t *testing.T, m *extension.Module, children int) time.Duration {
	t.Helper()
	settings, err := json.Marshal(map[string]int{"children": children})
	if err != nil {
		t.Fatalf("marshalling settings: %v", err)
	}

	start := time.Now()
	out, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
		Caller:   v1.CallerFromSession("s"),
		Ref:      v1.ContentRef{Provider: "extprobe", NativeID: "tt0", MediaType: v1.MediaTVSeries},
		Settings: settings,
	})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("import with %d children: %v", children, err)
	}
	if want := 1 + children; out.Items != want {
		t.Fatalf("import made %d items, expected %d — the probe did not do the work being measured",
			out.Items, want)
	}
	return elapsed
}

// countingContent is the cheapest possible ContentService, so what is timed is
// the boundary rather than the Platform's real write path.
type countingContent struct {
	stubContentService
	works    int
	children int
}

func (c *countingContent) AddContentWork(context.Context, v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	c.works++
	return v1.AddContentWorkResult{Work: v1.Node{ID: v1.NodeID(fmt.Sprintf("w%d", c.works))}}, nil
}

func (c *countingContent) AddContentChild(_ context.Context, cmd v1.AddContentChildCommand) (v1.AddContentChildResult, error) {
	c.children++
	return v1.AddContentChildResult{Node: v1.Node{
		ID:     v1.NodeID(fmt.Sprintf("c%d", c.children)),
		WorkID: cmd.ParentID,
		Title:  cmd.Title,
	}}, nil
}
