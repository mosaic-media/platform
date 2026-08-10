// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// The egress slice proven end to end, through a real spawned module making a
// real HTTP call with an ordinary client (platform#39).
//
// extprobe fetches a URL during Import using http.Get — the same default-client
// shape a real module uses — and signals the outcome in the import's Parts
// count: 1 if the fetch was allowed, 0 if it was refused. What is under test is
// whether the call was *permitted*, not what it returned, so the count is what
// the test reads.
//
// The target is a loopback server, which is the case that matters and the one a
// naive HTTP_PROXY misses: Go's ProxyFromEnvironment bypasses the proxy for
// loopback, so this passing at all depends on sdk/host (host/v0.2.0) forcing the
// transport through the proxy regardless. If that force regressed, the default
// case here would start *allowing* the fetch — a loopback SSRF reopening — and
// this test would fail.

func fetchThroughProbe(t *testing.T, url string, allowPrivate bool) (allowed bool) {
	t.Helper()
	m, err := extension.Launch(extension.Config{
		BinaryPath:         buildProbe(t),
		Content:            &recordingContent{},
		AllowPrivateEgress: allowPrivate,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(m.Close)

	settings, err := json.Marshal(map[string]string{"fetch_url": url})
	if err != nil {
		t.Fatalf("settings: %v", err)
	}
	out, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
		Caller:   v1.CallerFromSession("h"),
		Ref:      v1.ContentRef{Provider: "extprobe", NativeID: "x", MediaType: v1.MediaMovie},
		Settings: settings,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	return out.Parts == 1
}

// The default refuses a module's fetch of a private target. This is the SSRF
// regression that running out of process would otherwise introduce — netguard's
// in-process dial guard does not cross the boundary — closed for a cooperating
// module, which every first-party module is.
func TestModuleEgressToAPrivateTargetIsDeniedByDefault(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer target.Close()

	if fetchThroughProbe(t, target.URL, false) {
		t.Fatal("a module reached a private (loopback) target — the egress deny list did not apply")
	}
}

// The operator override re-opens the LAN for the genuine case of a module
// sourcing from a local service, and then the same fetch succeeds — proving the
// deny above was the deny list at work, not the target being unreachable.
func TestModuleEgressToAPrivateTargetSucceedsUnderTheOverride(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))
	defer target.Close()

	if !fetchThroughProbe(t, target.URL, true) {
		t.Fatal("the override did not let the module reach a local target")
	}
}
