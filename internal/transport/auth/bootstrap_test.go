// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth_test

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	sduiv1 "github.com/mosaic-media/contracts/gen/mosaic/sdui/v1"
	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/transport/auth"
)

// The pre-session bootstrap (ADR 0101). What is worth testing hardest is not
// that it answers — a handler returning the whole library would also answer —
// but the three properties that are easy to lose: the subset stays a subset,
// negotiation applies, and nothing here varies on identity.

func bootstrap(t *testing.T, h *auth.Handler, vocab *sessionv1.VocabularyProfile) *authv1.BootstrapResponse {
	t.Helper()
	res, err := h.Bootstrap(context.Background(), connect.NewRequest(&authv1.BootstrapRequest{
		Vocabulary: vocab,
	}))
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	return res.Msg
}

// The whole of what a client needs to draw one screen, in one response — which
// is the point: split into two calls, each can succeed while the other does
// not, and that is exactly the failure this replaces.
func TestBootstrapAnswersWithTheSkinTheDefinitionsAndTheTree(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	msg := bootstrap(t, auth.NewHandler(newTestService(db, testNow)), nil)

	if len(msg.GetTokens()) == 0 {
		t.Error("no token set — the doorway would be the one screen drawn unstyled")
	}
	var tokenDoc map[string]json.RawMessage
	if err := json.Unmarshal(msg.GetTokens(), &tokenDoc); err != nil {
		t.Errorf("the token set is not a JSON document: %v", err)
	}
	if _, ok := tokenDoc["base"]; !ok {
		t.Error("the token set carries no base group")
	}

	if len(msg.GetDefinitions()) == 0 {
		t.Error("no definitions — every component in the tree would draw as a placeholder")
	}
	if msg.GetUiNode() == nil {
		t.Fatal("no tree")
	}
	if msg.GetUiNode().GetType() != "Screen" {
		t.Errorf("the doorway is rooted at %q, want Screen", msg.GetUiNode().GetType())
	}
}

// A doorway has two states and the server picks. This is ADR 0098's decision,
// carried unchanged.
func TestBootstrapServesSetupWhileUnclaimedAndSignInOnceClaimed(t *testing.T) {
	unclaimed := bootstrap(t, auth.NewHandler(newTestService(newFakeDB(), testNow)), nil)
	if got := treeText(unclaimed.GetUiNode()); !strings.Contains(got, "has not been set up") {
		t.Errorf("an unclaimed server served: %s", got)
	}

	db := newFakeDB()
	signedInUser(db)
	claimed := bootstrap(t, auth.NewHandler(newTestService(db, testNow)), nil)
	if got := treeText(claimed.GetUiNode()); !strings.Contains(got, "Sign in") {
		t.Errorf("a claimed server served: %s", got)
	}
}

// The subset is the security property of the one payload an unauthenticated
// party can enumerate. A response carrying the library would pass every other
// test in this file.
func TestBootstrapServesADefinitionSubsetAndNotTheLibrary(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	msg := bootstrap(t, auth.NewHandler(newTestService(db, testNow)), nil)

	var served []map[string]json.RawMessage
	if err := json.Unmarshal(msg.GetDefinitions(), &served); err != nil {
		t.Fatalf("decode definitions: %v", err)
	}
	if len(served) == 0 {
		t.Fatal("the doorway was served no definitions at all")
	}
	// The doorway is three components. The library is dozens; a response that
	// grew towards it is the failure, whatever the exact number is on the day.
	if len(served) > 6 {
		names := make([]string, 0, len(served))
		for _, d := range served {
			var n string
			_ = json.Unmarshal(d["name"], &n)
			names = append(names, n)
		}
		t.Fatalf("the doorway disclosed %d definitions (%s) — the subset has grown into the library",
			len(served), strings.Join(names, ", "))
	}
}

// ADR 0084's negotiation applies to the doorway exactly as it applies to every
// screen after it, because the request carries the same declaration Attach
// carries. A client that declares nothing is an older client and gets
// everything, which is the compatibility guarantee.
func TestBootstrapAppliesTheDeclaredVocabulary(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	h := auth.NewHandler(newTestService(db, testNow))

	// A client that declares every primitive gets templates with no fallback
	// swapped in; the `fallback` key is stripped either way, because a client
	// never chooses — the server already did.
	all := make([]string, 0, len(sdui.Primitives))
	for _, p := range sdui.Primitives {
		all = append(all, p.Type)
	}
	full := bootstrap(t, h, &sessionv1.VocabularyProfile{Version: "3.0.0", Primitives: all})
	var defs []map[string]json.RawMessage
	if err := json.Unmarshal(full.GetDefinitions(), &defs); err != nil {
		t.Fatalf("decode definitions: %v", err)
	}
	for _, d := range defs {
		if _, ok := d["fallback"]; ok {
			t.Error("a served definition still carries its fallback; the client would have to choose")
		}
	}

	// And a client declaring nothing still gets a usable payload rather than an
	// empty one — an undeclared vocabulary is not a claim to render nothing.
	none := bootstrap(t, h, nil)
	if len(none.GetDefinitions()) == 0 {
		t.Error("an undeclared client was served no definitions")
	}
}

// It does not vary on the identity attempted, because it takes none. The
// response for a server with one account and a server with several is the same
// bytes, so nothing here can be used to learn who exists.
func TestBootstrapDoesNotVaryOnWhoExists(t *testing.T) {
	one := newFakeDB()
	signedInUser(one)
	first := bootstrap(t, auth.NewHandler(newTestService(one, testNow)), nil)

	many := newFakeDB()
	signedInUser(many)
	many.seedUser(domain.User{
		ID: "user-second", Username: "someone-else", Email: "b@example.com",
		Status: domain.UserActive, CreatedAt: testNow, UpdatedAt: testNow,
	})
	second := bootstrap(t, auth.NewHandler(newTestService(many, testNow)), nil)

	if treeText(first.GetUiNode()) != treeText(second.GetUiNode()) {
		t.Error("the doorway differs between a server with one account and one with two")
	}
	if string(first.GetDefinitions()) != string(second.GetDefinitions()) {
		t.Error("the served definitions differ between two claimed servers")
	}
}

// It is the one surface reachable before authentication, so it is bounded.
func TestBootstrapIsRateLimitedPerPeer(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	h := auth.NewHandler(newTestService(db, testNow))
	now := testNow
	auth.SetClockForTest(h, func() time.Time { return now })

	req := func() error {
		_, err := h.Bootstrap(context.Background(), connect.NewRequest(&authv1.BootstrapRequest{}))
		return err
	}

	// The burst is spendable in full — a limit a real client can reach is a
	// limit that gets raised without thought.
	for i := 0; i < auth.BootstrapBurstForTest; i++ {
		if err := req(); err != nil {
			t.Fatalf("request %d of the burst was refused: %v", i+1, err)
		}
	}
	err := req()
	if err == nil {
		t.Fatal("an unbounded caller was served past the burst")
	}
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("CodeOf(err) = %v, want ResourceExhausted", connect.CodeOf(err))
	}

	// And it refills, so a client that waited is served rather than locked out.
	now = now.Add(time.Minute)
	if err := req(); err != nil {
		t.Fatalf("the bucket did not refill: %v", err)
	}
}

// treeText flattens every string a tree carries, so a test can assert what a
// doorway says without reaching into the shape it says it in.
func treeText(n *sduiv1.UINode) string {
	if n == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString(n.GetType())
	b.WriteString(" ")
	// Sorted, because a props bag is a Go map and an unsorted walk would make
	// this comparison a coin flip — which is how the identity test first
	// "failed".
	props := n.GetProps().AsMap()
	keys := make([]string, 0, len(props))
	for k := range props {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := props[k].(string); ok {
			b.WriteString(k)
			b.WriteString("=")
			b.WriteString(s)
			b.WriteString(" ")
		}
	}
	for _, c := range n.GetChildren() {
		b.WriteString(treeText(c))
	}
	for _, list := range n.GetSlots() {
		for _, c := range list.GetNodes() {
			b.WriteString(treeText(c))
		}
	}
	return b.String()
}
