// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	aiostreams "github.com/mosaic-media/module-aiostreams"
	stremio "github.com/mosaic-media/module-stremio-addons"
	tmdb "github.com/mosaic-media/module-tmdb"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TestAIOStreamsEnrichesATMDBTitleAgainstPostgres is the AIOStreams module doing
// the only job it has, with the real module, the real enrichment pass and a real
// database.
//
// It is worth its own test rather than a variation of the Stremio one because
// the module's shape is different in a way that could silently not work: it
// fills *no* read role, so nothing can route an import to it and it is only ever
// reached through ADR 0073's enrichment fan-out. If that fan-out did not run, or
// ran without offering the IMDB identity, this module would look installed and
// contribute nothing — with no error anywhere.
//
// A second module is registered alongside it deliberately: the precedence claim
// in `registerCapabilities` — that AIOStreams is asked first because module ids
// sort that way — is otherwise a comment nothing checks.
func TestAIOStreamsEnrichesATMDBTitleAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	// The metadata source: a series with one season of two episodes, carrying the
	// IMDB id that is the only identity these modules share.
	metadata := fakeTMDBForEnrichment()
	defer metadata.Close()

	instance := fakeAIOStreamsInstance()
	defer instance.Close()

	// The Stremio module, configured and working, so the ordering assertion is
	// about precedence rather than about one source being the only one able to
	// answer.
	addon := fakeStremioAddon()
	defer addon.Close()

	registry := app.NewCapabilityRegistry()
	registry.Register(tmdb.New(redirectTo(metadata)))
	registry.Register(aiostreams.New(instance.Client()))
	registry.Register(stremio.New(addon.Client()))
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

	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: tmdb.CapabilityID,
		Settings: []byte(`{"apiKey":"0123456789abcdef0123456789abcdef"}`),
	}); err != nil {
		t.Fatalf("ConfigureModule(tmdb): %v", err)
	}
	// The instance URL as a user pastes it: the manifest URL, profile segments
	// and all. Normalisation happens inside the module.
	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: aiostreams.CapabilityID,
		Settings: []byte(`{"instanceUrl":"` + instance.URL + `/stremio/profile-1/secret/manifest.json"}`),
	}); err != nil {
		t.Fatalf("ConfigureModule(aiostreams): %v", err)
	}
	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: stremio.CapabilityID,
		Settings: []byte(`{"addons":["` + addon.URL + `"]}`),
	}); err != nil {
		t.Fatalf("ConfigureModule(stremio): %v", err)
	}

	// Import a *TMDB* ref. No stream source is named anywhere in this command.
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
	if result.Parts == 0 {
		t.Fatal("no parts attached: the AIOStreams module was registered and never contributed")
	}

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
		// The precedence claim, checked rather than asserted in a comment: both
		// stream sources can answer, and module-id order decides.
		if part.Location.Provider != aiostreams.CapabilityID {
			t.Errorf("episode %q resolved through %q, want %q — module-id order is the precedence rule",
				episode.Title, part.Location.Provider, aiostreams.CapabilityID)
		}
		// The Platform never built a Stremio-style episode id. The module composed
		// it from the shared IMDB id plus the season and episode numbers the
		// Platform read off the tree — which is the whole point of the coordinates
		// on StreamRequest.
		if !strings.Contains(part.Location.Ref, "tt0903747:1:") {
			t.Errorf("episode %q ref = %q, want an id composed from the shared IMDB identity",
				episode.Title, part.Location.Ref)
		}
	}
}

// TestAIOStreamsDeclinesWhenItsInstanceIsUnconfigured is the state a fresh
// install is in, all the way through the Platform: the default instance is up
// and has no profile, so it serves nothing.
//
// The assertion is that the import *succeeds* — a stream source with nothing to
// offer must not lose a user the title they just added. It is the same rule as a
// metadata-only deployment, arriving by a different route.
func TestAIOStreamsDeclinesWhenItsInstanceIsUnconfigured(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	metadata := fakeTMDBForEnrichment()
	defer metadata.Close()
	instance := fakeAIOStreamsInstance()
	defer instance.Close()

	registry := app.NewCapabilityRegistry()
	registry.Register(tmdb.New(redirectTo(metadata)))
	registry.Register(aiostreams.New(instance.Client()))
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

	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: tmdb.CapabilityID,
		Settings: []byte(`{"apiKey":"0123456789abcdef0123456789abcdef"}`),
	}); err != nil {
		t.Fatalf("ConfigureModule(tmdb): %v", err)
	}
	// Pointed at the instance root, which is what the public default is: up, and
	// with no profile behind it.
	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: aiostreams.CapabilityID,
		Settings: []byte(`{"instanceUrl":"` + instance.URL + `/stremio"}`),
	}); err != nil {
		t.Fatalf("ConfigureModule(aiostreams): %v", err)
	}

	result, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller,
		Ref: v1.ContentRef{
			Provider: tmdb.CapabilityID, NativeID: "1396", NativeType: "tv",
			MediaType: v1.MediaTVSeries, ExternalScheme: "tmdb", ExternalID: "1396",
		},
	})
	if err != nil {
		t.Fatalf("an unconfigured stream source must not fail the import: %v", err)
	}
	if result.Items != 2 {
		t.Fatalf("tree = %d items, want the 2 the metadata module built", result.Items)
	}
	if result.Parts != 0 {
		t.Fatalf("attached %d parts from an instance that declares no stream resource", result.Parts)
	}
}

// fakeAIOStreamsInstance serves both states of a real instance: a configured
// profile under a two-segment path, and the bare instance root that declares
// `configurationRequired` and nothing else.
func fakeAIOStreamsInstance() *httptest.Server {
	const profile = "/stremio/profile-1/secret"
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == profile+"/manifest.json":
			_, _ = w.Write([]byte(`{"id":"com.aiostreams.viren070","name":"AIOStreams","version":"2.31.1",
				"types":["movie","series"],
				"resources":[{"name":"stream","types":["movie","series"],"idPrefixes":["tt"]}],
				"behaviorHints":{"configurable":true}}`))
		case path == "/stremio/manifest.json":
			_, _ = w.Write([]byte(`{"id":"com.aiostreams.viren070","name":"AIOStreams","version":"2.31.1",
				"catalogs":[],"resources":[],"types":[],
				"behaviorHints":{"configurable":true,"configurationRequired":true}}`))
		case strings.HasPrefix(path, profile+"/stream/"):
			id := strings.TrimSuffix(path[len(profile+"/stream/"):], ".json")
			_, _ = w.Write([]byte(`{"streams":[{"name":"[RD+] AIOStreams 1080p",
				"title":"Breaking Bad 1080p WEB-DL x264 EAC3\n💾 3.1 GB 👤 24",
				"url":"http://cdn.example/` + id + `"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}
