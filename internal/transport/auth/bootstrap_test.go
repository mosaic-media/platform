// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth_test

import (
	"net/http"
	"net/http/httptest"

	"context"
	"encoding/json"
	"github.com/mosaic-media/contracts/gen/mosaic/auth/v1/authv1connect"
	"github.com/mosaic-media/platform/internal/transport/clientaddr"
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
	"github.com/mosaic-media/platform/internal/transport/vocabulary"
)

// The pre-session bootstrap (platform#57). What is worth testing hardest is not
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

// A doorway has two states and the server picks. This is platform#54's decision,
// carried unchanged.
func TestBootstrapServesSetupWhileUnclaimedAndSignInOnceClaimed(t *testing.T) {
	unclaimed := bootstrap(t, auth.NewHandler(newTestService(newFakeDB(), testNow)), nil)
	if got := treeText(unclaimed.GetUiNode()); !strings.Contains(got, "Set up Mosaic") {
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
	names := make([]string, 0, len(served))
	for _, d := range served {
		var n string
		_ = json.Unmarshal(d["name"], &n)
		names = append(names, n)
	}

	// Bounded against the library rather than against a number somebody
	// remembered to update. The count moved once already — the doorway grew a
	// form — and a hard cap would have failed for the right shape of change,
	// which teaches the next person to raise it rather than to look.
	var library []map[string]json.RawMessage
	if err := json.Unmarshal(vocabulary.Library(), &library); err != nil {
		t.Fatalf("decode the library: %v", err)
	}
	if len(served) >= len(library) {
		t.Fatalf("the doorway disclosed %d of %d definitions (%s) — the subset has become the library",
			len(served), len(library), strings.Join(names, ", "))
	}

	// And named, because a count alone would pass for a subset that happened to
	// carry the wrong half. Nothing about the library a signed-in person browses
	// should be enumerable from a door.
	for _, leaked := range []string{"PosterCard", "AppShell", "DetailHero", "SettingsFrame", "LogTable"} {
		for _, n := range names {
			if n == leaked {
				t.Errorf("the doorway disclosed %q, which no door draws", leaked)
			}
		}
	}
}

// platform#52's negotiation applies to the doorway exactly as it applies to every
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
// The burst and the refill. It is deliberately not named "per peer": every
// request here comes from the same (absent) caller, so separation between
// callers is the *next* test's job — this one was called that for a while and
// checked no such thing.
func TestBootstrapBurstIsSpendableAndRefills(t *testing.T) {
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

// Separation between callers, over real HTTP, because that is the only place
// the chain that produces it exists: the middleware resolves an address, the
// handler keys a bucket on it, and neither half is meaningful alone.
//
// It matters more since platform#75 than it did before. Every request now arrives
// from the Supervisor over a Unix socket, which has no peer address at all, so
// without the forwarded address one household shares one bucket and any member
// of it can spend everyone's.
func TestTwoCallersGetSeparateBuckets(t *testing.T) {
	db := newFakeDB()
	signedInUser(db)
	h := auth.NewHandler(newTestService(db, testNow))
	now := testNow
	auth.SetClockForTest(h, func() time.Time { return now })

	path, connectHandler := authv1connect.NewAuthServiceHandler(h)
	mux := http.NewServeMux()
	mux.Handle(path, connectHandler)
	// Trusted, as it is on a socket the front door alone can reach.
	server := httptest.NewServer(clientaddr.Middleware(true)(mux))
	defer server.Close()

	client := authv1connect.NewAuthServiceClient(server.Client(), server.URL)
	call := func(caller string) error {
		req := connect.NewRequest(&authv1.BootstrapRequest{})
		req.Header().Set("X-Forwarded-For", caller)
		_, err := client.Bootstrap(context.Background(), req)
		return err
	}

	// One caller spends its whole burst and is then refused.
	for i := 0; i < auth.BootstrapBurstForTest; i++ {
		if err := call("198.51.100.1"); err != nil {
			t.Fatalf("request %d of the first caller's burst was refused: %v", i+1, err)
		}
	}
	if err := call("198.51.100.1"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("the first caller was not limited: %v", err)
	}

	// A second caller is unaffected. Before the forwarded address was read,
	// both were the Supervisor and this request would have been refused.
	if err := call("198.51.100.2"); err != nil {
		t.Fatalf("a second caller was refused because the first had spent its budget: %v", err)
	}

	// And a caller cannot escape its own bucket by prepending an address it
	// made up: the front door's entry is the last one.
	if err := call("10.0.0.99, 198.51.100.1"); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("a forged prefix bought a fresh bucket: %v", err)
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
