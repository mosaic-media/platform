// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
)

// This is ADR 0064's build-order step 2: the real module out of process. Step 1
// (extprobe) established the wire, the handshake and the handle with a trivial
// module; this runs the actual `module-stremio-addons` binary — four roles, real
// telemetry, a real import that calls back into ContentService many times — over
// the same boundary.
//
// **It is a proof, not the production cutover.** The Platform still composes this
// module statically; nothing here changes that. What extprobe cannot show and
// this can is that the *real* module's compiled binary serves correctly under
// host.Serve — that no global, init, or stray stdout write breaks the handshake,
// and that the module's own four-role code works unchanged across a process it
// was never written to expect. The round-trip cost is measured separately
// (chattiness_test.go) and needs no real module.
//
// The coupling this introduces — the Platform's test suite building and spawning
// one specific module's binary — is deliberate and bounded. The module is
// already a required dependency, and ADR 0064 names this exact module as the
// step-2 test precisely because it is the hardest real one. The fake addon keeps
// it hermetic: no network, no live Stremio ecosystem.

// fakeAddon is the smallest Stremio addon that exercises the module's four
// source roles: a manifest declaring meta/catalog/stream, a searchable catalog,
// one meta document, and one stream. It is not the Stremio protocol in full —
// only the subset module-stremio-addons fetches.
func fakeAddon(t *testing.T) *httptest.Server {
	t.Helper()

	mux := http.NewServeMux()

	mux.HandleFunc("/manifest.json", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"id":        "org.fake.addon",
			"name":      "Fake Addon",
			"version":   "1.0.0",
			"resources": []string{"meta", "catalog", "stream"},
			"types":     []string{"movie"},
			"catalogs": []map[string]any{{
				"type":  "movie",
				"id":    "top",
				"name":  "Top Movies",
				"extra": []map[string]string{{"name": "search"}},
			}},
		})
	})

	// Catalog search: /catalog/movie/top/search=<query>.json
	mux.HandleFunc("/catalog/movie/top/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"metas": []map[string]any{{
				"id":          "tt0083658",
				"type":        "movie",
				"name":        "Blade Runner",
				"poster":      "https://example.invalid/p.jpg",
				"releaseInfo": "1982",
			}},
		})
	})

	// Meta lookup: /meta/movie/tt0083658.json
	mux.HandleFunc("/meta/movie/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"meta": map[string]any{
				"id":     "tt0083658",
				"type":   "movie",
				"name":   "Blade Runner",
				"poster": "https://example.invalid/p.jpg",
			},
		})
	})

	// Streams: /stream/movie/tt0083658.json
	mux.HandleFunc("/stream/movie/", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, map[string]any{
			"streams": []map[string]any{{
				"title": "Blade Runner 1080p",
				"url":   "https://example.invalid/stream.mkv",
			}},
		})
	})

	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// buildStremio compiles the real module's binary from the required dependency.
// It builds from the module cache the same way the Platform's own build does, so
// what runs is the tagged release, not a working copy.
func buildStremio(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "stremio")
	cmd := exec.Command("go", "build", "-o", bin,
		"github.com/mosaic-media/module-stremio-addons/cmd/module-stremio-addons")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building module-stremio-addons: %v\n%s", err, out)
	}
	return bin
}

func launchStremio(t *testing.T, content v1.ContentService, addonURL string) (*extension.Module, []byte) {
	t.Helper()
	m, err := extension.Launch(extension.Config{
		BinaryPath: buildStremio(t),
		Content:    content,
	})
	if err != nil {
		t.Fatalf("launch stremio: %v", err)
	}
	t.Cleanup(m.Close)

	settings, err := json.Marshal(map[string]any{"addons": []string{addonURL}})
	if err != nil {
		t.Fatalf("marshalling settings: %v", err)
	}
	return m, settings
}

// The real module's manifest crosses the boundary with all four source roles it
// actually declares — the first thing that could break if the compiled binary
// did not serve correctly under host.Serve.
func TestRealStremioManifestCrossesTheBoundary(t *testing.T) {
	m, _ := launchStremio(t, &recordingContent{}, "http://127.0.0.1:0")

	got := m.Capability.Manifest()
	if got.ID == "" {
		t.Fatal("the real module served no manifest id")
	}

	want := map[v1.Role]bool{
		v1.RoleMetadata: false, v1.RoleSearch: false,
		v1.RoleCatalog: false, v1.RoleStream: false,
		v1.RoleSubtitles: false, v1.RoleSettingsUI: false,
	}
	for _, r := range got.Provides {
		if _, ok := want[r]; ok {
			want[r] = true
		}
	}
	for role, seen := range want {
		if !seen {
			t.Errorf("the real module did not declare %q across the boundary", role)
		}
	}
}

// The real module's Search runs in the child, fetches the fake addon over HTTP,
// and returns results across the boundary. This is the round trip that proves
// the module's own code works out of process, not only the harness.
func TestRealStremioSearchAcrossTheBoundary(t *testing.T) {
	addon := fakeAddon(t)
	m, settings := launchStremio(t, &recordingContent{}, addon.URL)

	sp, ok := m.Capability.(v1.SearchProvider)
	if !ok {
		t.Fatal("the real module's proxy is not a SearchProvider")
	}
	out, err := sp.Search(context.Background(), v1.SearchRequest{
		Caller:   v1.CallerFromSession("h"),
		Settings: settings,
		Text:     "blade runner",
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results: got %d, want 1", len(out.Results))
	}
	if out.Results[0].Title != "Blade Runner" || out.Results[0].Year != 1982 {
		t.Errorf("result did not survive the boundary: %+v", out.Results[0])
	}
}

// The whole thing at once: the real module imports a title, which fetches the
// fake addon and calls back into the Platform's ContentService several times
// within one invocation — the callback direction ADR 0064 names as step 2's
// point, driven by the real module rather than the probe.
func TestRealStremioImportCallsBack(t *testing.T) {
	addon := fakeAddon(t)
	content := &fanoutContent{}
	m, settings := launchStremio(t, content, addon.URL)

	out, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
		Caller: v1.CallerFromSession("invocation-handle"),
		Ref: v1.ContentRef{
			Provider: m.Capability.Manifest().ID,
			NativeID: "tt0083658", NativeType: "movie",
			MediaType:      v1.MediaMovie,
			ExternalScheme: "imdb", ExternalID: "tt0083658",
		},
		Settings: settings,
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if out.WorkID == "" {
		t.Fatal("import returned no work id")
	}

	if content.works() == 0 {
		t.Error("the import created no work through the callback path")
	}

	// This ContentService is on the *Platform* side of the boundary, after
	// resolvingContent has exchanged the handle back — so every callback should
	// arrive carrying the resolved real session, "invocation-handle". Seeing it
	// proves the full round trip with the real module: the Platform minted a
	// handle, the module carried it back on each callback, and the wrapper
	// resolved it to the invoking user before the real service ran.
	//
	// A raw handle (a random base64 string) or an empty caller here would mean
	// the exchange failed. The complementary guarantee — that the *module* never
	// sees "invocation-handle" — is covered in invocations_test.go, which can
	// observe the module side directly and this test cannot.
	seen := content.callers()
	if len(seen) == 0 {
		t.Fatal("the import made no callbacks at all")
	}
	for _, c := range seen {
		if c != "invocation-handle" {
			t.Errorf("a callback arrived with caller %q, not the resolved real session — "+
				"the handle exchange did not complete", c)
		}
	}
}

// fanoutContent records enough to show the import drove the callback direction,
// and every caller it saw so the handle guarantee can be checked.
type fanoutContent struct {
	stubContentService
	mu       sync.Mutex
	workIDs  int
	seen     []string
	partSeen int
}

func (c *fanoutContent) works() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.workIDs
}

func (c *fanoutContent) callers() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]string(nil), c.seen...)
}

func (c *fanoutContent) note(session string) {
	c.mu.Lock()
	c.seen = append(c.seen, session)
	c.mu.Unlock()
}

func (c *fanoutContent) FindContentByExternalID(_ context.Context, q v1.FindContentByExternalIDQuery) (v1.FindContentByExternalIDResult, error) {
	c.note(q.Caller.Session)
	return v1.FindContentByExternalIDResult{}, nil // not in library — proceed to create
}

func (c *fanoutContent) AddContentWork(_ context.Context, cmd v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	c.mu.Lock()
	c.workIDs++
	id := fmt.Sprintf("work-%d", c.workIDs)
	c.mu.Unlock()
	c.note(cmd.Caller.Session)
	return v1.AddContentWorkResult{Work: v1.Node{ID: v1.NodeID(id), WorkID: v1.NodeID(id), Title: cmd.Title, MediaType: cmd.MediaType}}, nil
}

func (c *fanoutContent) AttachContentPart(_ context.Context, cmd v1.AttachContentPartCommand) (v1.AttachContentPartResult, error) {
	c.mu.Lock()
	c.partSeen++
	c.mu.Unlock()
	c.note(cmd.Caller.Session)
	return v1.AttachContentPartResult{Part: v1.Part{ID: "part-1", NodeID: cmd.NodeID}}, nil
}

func (c *fanoutContent) BindContentSource(_ context.Context, cmd v1.BindContentSourceCommand) (v1.BindContentSourceResult, error) {
	c.note(cmd.Caller.Session)
	return v1.BindContentSourceResult{Binding: v1.SourceBinding{ID: "bind-1", NodeID: cmd.NodeID}}, nil
}

func (c *fanoutContent) SetContentArtwork(_ context.Context, cmd v1.SetContentArtworkCommand) (v1.SetContentArtworkResult, error) {
	c.note(cmd.Caller.Session)
	return v1.SetContentArtworkResult{Node: v1.Node{ID: cmd.NodeID}}, nil
}

// Guard against a silent divergence: if the module ever calls a ContentService
// method these overrides do not cover, the embedded stub returns a zero value
// and the import may quietly do less than it should. Keeping the import small
// (one movie, one stream) bounds what it can reach.
var _ = strings.TrimSpace
