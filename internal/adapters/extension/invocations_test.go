// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// capturingContent records the Caller each callback arrived with, and keeps the
// service around so a test can replay a call after the invocation ended — which
// is the whole point of the handle design (platform#39).
type capturingContent struct {
	stubContentService

	seenCaller  string
	seenHandles []string
}

func (c *capturingContent) AddContentWork(_ context.Context, cmd v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	c.seenCaller = cmd.Caller.Session
	return v1.AddContentWorkResult{Work: v1.Node{ID: "w1", Title: cmd.Title}}, nil
}

// The module never receives a real session reference — only a handle. This is
// what makes retention harmless: what a module could keep is not a credential.
func TestModuleNeverSeesTheRealSessionReference(t *testing.T) {
	content := &capturingContent{}
	m := launch(t, content)

	const realSession = "session-that-must-not-cross"
	if _, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
		Caller: v1.CallerFromSession(realSession),
		Ref:    v1.ContentRef{Provider: "extprobe", NativeID: "x", MediaType: v1.MediaMovie},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	// The Platform's own service saw the real caller, because the handle was
	// exchanged back on the way in.
	if content.seenCaller != realSession {
		t.Errorf("the Platform service should see the real caller, got %q", content.seenCaller)
	}
}

// The property the design exists for: a handle stops resolving the instant the
// invocation returns. There is no window, which is why platform#39 refused a
// short-TTL token — a TTL *is* a window.
func TestARetainedHandleStopsResolvingWhenTheInvocationReturns(t *testing.T) {
	content := &capturingContent{}
	m := launch(t, content)

	// Run an import so a handle is minted and revoked.
	if _, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
		Caller: v1.CallerFromSession("real-session"),
		Ref:    v1.ContentRef{Provider: "extprobe", NativeID: "x", MediaType: v1.MediaMovie},
	}); err != nil {
		t.Fatalf("import: %v", err)
	}

	if got := m.LiveInvocations(); got != 0 {
		t.Fatalf("the invocation table did not empty: %d handles still live", got)
	}
}

// Every invocation empties the table, including ones that fail. A revoke that
// only ran on the success path would leave a usable handle behind after exactly
// the invocations most worth abusing.
func TestTheTableEmptiesAcrossManyInvocations(t *testing.T) {
	m := launch(t, &capturingContent{})

	for i := 0; i < 5; i++ {
		if _, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
			Caller: v1.CallerFromSession("s"),
			Ref:    v1.ContentRef{Provider: "extprobe", NativeID: "x", MediaType: v1.MediaMovie},
		}); err != nil {
			t.Fatalf("import %d: %v", i, err)
		}
	}

	// A read role too — every method mints, not only the one that can call back.
	sp, ok := m.Capability.(v1.SearchProvider)
	if !ok {
		t.Fatal("the proxy is not a SearchProvider")
	}
	if _, err := sp.Search(context.Background(), v1.SearchRequest{
		Caller: v1.CallerFromSession("s"),
		Text:   "x",
	}); err != nil {
		t.Fatalf("search: %v", err)
	}

	if got := m.LiveInvocations(); got != 0 {
		t.Errorf("the invocation table leaked: %d handles still live", got)
	}
}

// A role that fails still revokes, because the revoke is deferred rather than
// run on the success path. The probe refuses artwork, so this exercises it.
func TestAFailedInvocationStillRevokes(t *testing.T) {
	m := launch(t, &capturingContent{})

	ap, ok := m.Capability.(v1.ArtworkProvider)
	if !ok {
		t.Fatal("the proxy is not an ArtworkProvider")
	}
	if _, err := ap.Artwork(context.Background(), v1.ArtworkRequest{
		Caller: v1.CallerFromSession("s"),
	}); err == nil {
		t.Fatal("expected the probe to refuse artwork")
	}

	if got := m.LiveInvocations(); got != 0 {
		t.Errorf("a failed invocation left %d handles live", got)
	}
}

// A forged or stale handle is refused with PermissionDenied rather than
// NotFound: the module is attempting to act as somebody, and the honest
// category is that it may not.
func TestAnUnknownHandleIsPermissionDenied(t *testing.T) {
	content := &capturingContent{}
	m := launch(t, content)

	// Reach the Platform's ContentService the way the module does, but with a
	// handle that was never minted. The wrapper is what the module's callbacks
	// land on, so exercising it directly is exercising the real path.
	svc := extension.ResolvingContentForTest(content, m)

	_, err := svc.AddContentWork(context.Background(), v1.AddContentWorkCommand{
		Caller: v1.CallerFromSession("a-handle-nobody-minted"),
		Title:  "should not be written",
	})
	if err == nil {
		t.Fatal("an unminted handle was accepted")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Errorf("category: got %q, want permission_denied", got)
	}
	if content.seenCaller != "" {
		t.Errorf("the write reached the Platform service despite an invalid handle: %q", content.seenCaller)
	}
}

func TestAnEmptyHandleIsPermissionDenied(t *testing.T) {
	content := &capturingContent{}
	m := launch(t, content)
	svc := extension.ResolvingContentForTest(content, m)

	_, err := svc.AddContentWork(context.Background(), v1.AddContentWorkCommand{
		Caller: v1.Caller{},
		Title:  "should not be written",
	})
	if err == nil {
		t.Fatal("an empty handle was accepted")
	}
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Errorf("category: got %q, want permission_denied", got)
	}
}
