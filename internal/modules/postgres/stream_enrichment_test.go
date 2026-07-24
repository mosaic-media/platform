// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	tmdb "github.com/mosaic-media/module-tmdb"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TestCrossProviderStreamEnrichmentAgainstPostgres is ADR 0073 end to end: a real
// metadata module (TMDB, core), a real database, and a stream source standing in
// for an extension one.
//
// It is the case the two-tier module story has been pointing at: **a title
// described by TMDB and played from a stream source.** Before this, importing a
// TMDB ref produced a Work and a season/episode tree with no Parts — permanently
// unplayable, while a stream source sat registered alongside able to resolve
// that exact title and never asked, because `ImportContent` routed solely to
// `ref.Provider`.
//
// The stream source is a fake, not the real Stremio module: the platform module
// must not import an extension module (ADR 0079/0081), and the Platform bridge is
// what is under test, not Stremio's addon parsing. The double answers *only* for
// the IMDB identity the Platform must have carried across from the TMDB tree, so
// the assertion stays real — Parts appear on those episodes only if the Platform
// bridged TMDB's metadata to the stream source's resolution. TMDB fills no stream
// role and attaches no Parts; the stream source never sees the import, because
// the ref names `tmdb`.
func TestCrossProviderStreamEnrichmentAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	// The metadata source: a series with one season of two episodes, carrying
	// the IMDB id that is the only identity the two modules share.
	metadata := fakeTMDBForEnrichment()
	defer metadata.Close()

	// The stream source. It answers only for the IMDB identity — the id the
	// Platform must have read off the TMDB tree and carried across — so a Part
	// appearing on an episode proves the bridge, not the double.
	streamSource := &fakeStreamModule{
		id: "stremio",
		streamsFor: func(req v1.StreamRequest) []v1.StreamLink {
			if req.Ref.ExternalScheme != "imdb" || req.Ref.ExternalID != "tt0903747" {
				return nil
			}
			return []v1.StreamLink{{
				Label:     "test-source",
				Location:  v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: "stremio", Ref: "magnet:?xt=urn:btih:deadbeef"},
				SizeBytes: 1,
			}}
		},
	}

	registry := app.NewCapabilityRegistry()
	registry.Register(tmdb.New(redirectTo(metadata)))
	registry.Register(streamSource)
	if err := registry.Verify(); err != nil {
		t.Fatalf("registry invalid: %v", err)
	}

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities: registry, ModuleSettings: cs.ModuleSettings,
	})

	now := cs.Clock.Now()
	user, err := cs.Users.Create(c, domain.User{ID: "importer", Username: "importer", Email: "importer@example.com", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	session, err := cs.Sessions.Create(c, domain.Session{
		ID: "importer-session", UserID: user.ID, DeviceID: "importer-device",
		IssuedAt: now, LastSeenAt: now, ExpiresAt: now.Add(time.Hour), AuthStrength: domain.AuthStrengthPassword,
	})
	if err != nil {
		t.Fatalf("seed session: %v", err)
	}
	if err := seedRoleGrant(c, pool, user.ID, "Importer", []domain.Permission{
		domain.Permission(app.ActionContentImport),
		domain.Permission(app.ActionContentCreate),
		domain.Permission(app.ActionContentBind),
		domain.Permission(app.ActionContentRead),
		domain.Permission(app.ActionModuleConfigure),
	}); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	caller := v1.CallerFromSession(string(session.ID))

	// TMDB is configured as a user would: it needs a key. The stream source needs
	// no settings — the double reads none — so, as with a real source that needs
	// no configuration, nothing is set for it.
	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: tmdb.CapabilityID,
		Settings: []byte(`{"apiKey":"0123456789abcdef0123456789abcdef"}`),
	}); err != nil {
		t.Fatalf("ConfigureModule(tmdb): %v", err)
	}

	// Import a *TMDB* ref. Stremio is not named anywhere in this command.
	result, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller,
		Ref: v1.ContentRef{
			Provider: tmdb.CapabilityID, NativeID: "1396", NativeType: "tv",
			MediaType: v1.MediaTVSeries, ExternalScheme: "tmdb", ExternalID: "1396",
		},
	})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}

	if result.Containers != 1 || result.Items != 2 {
		t.Fatalf("tree = %d containers, %d items; want 1/2 from the metadata module",
			result.Containers, result.Items)
	}

	// The assertion the whole record exists for. TMDB declares no stream role
	// and attaches nothing; these Parts came from Stremio, asked by the Platform.
	if result.Parts == 0 {
		t.Fatal("no parts attached: a TMDB-sourced series is still unplayable, so enrichment did not run")
	}

	// And they are really in the database, on the episodes, pointing at the
	// stream source.
	work, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: result.WorkID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(work): %v", err)
	}
	if len(work.Children) != 1 {
		t.Fatalf("work has %d children, want the one season", len(work.Children))
	}
	season, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: work.Children[0].ID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(season): %v", err)
	}
	if len(season.Children) != 2 {
		t.Fatalf("season has %d children, want 2 episodes", len(season.Children))
	}

	for _, episode := range season.Children {
		parts, err := svc.ListContentParts(c, v1.ListContentPartsQuery{Caller: caller, NodeID: episode.ID})
		if err != nil {
			t.Fatalf("ListContentParts(%s): %v", episode.Title, err)
		}
		if len(parts.Parts) == 0 {
			t.Fatalf("episode %q has no parts; enrichment missed it", episode.Title)
		}
		part := parts.Parts[0]
		if part.Location.Scheme != v1.RemoteLocation {
			t.Errorf("episode %q part scheme = %q, want remote", episode.Title, part.Location.Scheme)
		}
		// The Platform never built a Stremio episode id; the module composed it
		// from the shared IMDB id plus the season and episode numbers the
		// Platform read off the tree.
		if part.Location.Provider != "stremio" {
			t.Errorf("episode %q part provider = %q, want stremio", episode.Title, part.Location.Provider)
		}
	}

	// Re-importing must not attach a second copy of everything. Enrichment fills
	// gaps; it does not merge.
	before := countParts(t, c, svc, caller, season.Children)
	if _, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller,
		Ref: v1.ContentRef{
			Provider: tmdb.CapabilityID, NativeID: "1396", NativeType: "tv",
			MediaType: v1.MediaTVSeries, ExternalScheme: "tmdb", ExternalID: "1396",
		},
	}); err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}
	if after := countParts(t, c, svc, caller, season.Children); after != before {
		t.Fatalf("re-import changed the part count from %d to %d; enrichment is not idempotent", before, after)
	}
}

func countParts(t *testing.T, c context.Context, svc *app.Service, caller v1.Caller, episodes []v1.Node) int {
	t.Helper()
	total := 0
	for _, episode := range episodes {
		parts, err := svc.ListContentParts(c, v1.ListContentPartsQuery{Caller: caller, NodeID: episode.ID})
		if err != nil {
			t.Fatalf("ListContentParts: %v", err)
		}
		total += len(parts.Parts)
	}
	return total
}

// redirectTo sends every request to the test server regardless of the host the
// module asked for, which is how a module with a fixed upstream is pointed at a
// fake without putting a settable base URL in its production type.
func redirectTo(server *httptest.Server) *http.Client {
	base, _ := url.Parse(server.URL)
	return &http.Client{Transport: hostRewriter{base: base}}
}

type hostRewriter struct{ base *url.URL }

func (h hostRewriter) RoundTrip(req *http.Request) (*http.Response, error) {
	req = req.Clone(req.Context())
	req.URL.Scheme, req.URL.Host, req.Host = h.base.Scheme, h.base.Host, h.base.Host
	return http.DefaultTransport.RoundTrip(req)
}

// fakeTMDBForEnrichment serves the minimum TMDB surface an import needs: a
// series, its one season, and — critically — the IMDB id that is the only
// identity it shares with the stream source.
func fakeTMDBForEnrichment() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch path := r.URL.Path; {
		case path == "/3/tv/1396":
			_, _ = w.Write([]byte(`{
				"id":1396,"name":"Breaking Bad","overview":"A chemistry teacher.",
				"first_air_date":"2008-01-20","vote_average":8.9,
				"seasons":[{"season_number":1,"episode_count":2}],
				"external_ids":{"imdb_id":"tt0903747"},
				"credits":{"cast":[]},"images":{"logos":[]}}`))
		case path == "/3/tv/1396/season/1":
			_, _ = w.Write([]byte(`{"episodes":[
				{"episode_number":1,"name":"Pilot","air_date":"2008-01-20"},
				{"episode_number":2,"name":"Cat's in the Bag...","air_date":"2008-01-27"}]}`))
		case strings.HasPrefix(path, "/3/configuration"):
			_, _ = w.Write([]byte(`{"images":{"secure_base_url":"https://image.example/t/p/",
				"poster_sizes":["w500"],"backdrop_sizes":["w1280"],"logo_sizes":["w500"],
				"profile_sizes":["w185"],"still_sizes":["w300"]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"status_code":34,"status_message":"not found"}`))
		}
	}))
}
