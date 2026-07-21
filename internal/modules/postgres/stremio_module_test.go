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

	stremio "github.com/mosaic-media/mosaic-module-stremio"
	"github.com/mosaic-media/mosaic-platform/internal/modules/postgres"
	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// TestStremioModuleAgainstPostgres is the module slice end to end: the Platform
// invokes a registered, separately-compiled module through the ImportContent
// command, and the module — importing only the SDK — sources a series from a
// Stremio addon and lands its tree, source binding and RemoteLocation stream
// Parts in a real database, acting as its invoking user (ADR 0007, 0008, 0017).
//
// Unlike TestReferenceCapabilityAgainstPostgres, which calls the capability
// directly, this drives the composition-and-invocation path: registry ->
// ImportContent -> capability -> ContentService. That path is what this slice
// builds.
func TestStremioModuleAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	// A hermetic Stremio addon serving a series with one season of two episodes
	// and a direct-play stream for each.
	addon := fakeStremioAddon()
	defer addon.Close()

	// Register the module exactly as the composition root does, then build the
	// Service over that registry.
	registry := app.NewCapabilityRegistry()
	registry.Register(stremio.New(addon.Client()))

	svc := app.NewService(
		cs.UnitOfWork, cs.Sessions, cs.Users, cs.Credentials, cs.Config, cs.Permissions,
		cs.Nodes, cs.Clock, cs.IDs, cs.ContentIDs,
		policy.NewEngine(cs.Permissions), noopPublisher{}, reversibleVerifier{},
		registry,
		cs.ModuleSettings,
	)

	// An importer with a session and every action the invocation and the
	// capability's writes require, including content.import.
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
	actions := []domain.Permission{
		domain.Permission(app.ActionContentImport),
		domain.Permission(app.ActionContentCreate),
		domain.Permission(app.ActionContentBind),
		domain.Permission(app.ActionContentRead),
		domain.Permission(app.ActionModuleConfigure),
	}
	if err := seedRoleGrant(c, pool, user.ID, "Importer", actions); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	caller := v1.CallerFromSession(string(session.ID))

	// The user configures the module with the addon to source from — the gap-1
	// path: addons are user-managed settings (ADR 0021), not composed-in. The
	// bundled default (Cinemeta) is opted out so this stays hermetic against the
	// fake addon (ADR 0037) rather than importing the real series over the network.
	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: stremio.CapabilityID,
		Settings: []byte(`{"addons":["` + addon.URL + `"],"disableDefaultAddons":true}`),
	}); err != nil {
		t.Fatalf("ConfigureModule: %v", err)
	}

	// Invoke the module through the Platform's generic import command.
	result, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller, Ref: v1.ContentRef{Provider: stremio.CapabilityID, NativeID: "tt0903747", NativeType: "series", MediaType: v1.MediaTVSeries, ExternalScheme: "imdb", ExternalID: "tt0903747"},
	})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}
	if result.WorkID == "" || result.AlreadyKnown {
		t.Fatalf("expected a freshly created work, got %+v", result)
	}
	if result.Containers != 1 || result.Items != 2 || result.Parts != 2 {
		t.Fatalf("tree shape = %d containers, %d items, %d parts; want 1/2/2", result.Containers, result.Items, result.Parts)
	}

	// The work is findable by its IMDB id and is a tv_series.
	found, err := svc.FindContentByExternalID(c, v1.FindContentByExternalIDQuery{Caller: caller, Scheme: "imdb", Value: "tt0903747"})
	if err != nil {
		t.Fatalf("FindContentByExternalID: %v", err)
	}
	if len(found.Nodes) != 1 || found.Nodes[0].ID != result.WorkID {
		t.Fatalf("lookup = %+v, want the imported work", found.Nodes)
	}
	if found.Nodes[0].MediaType != v1.MediaTVSeries {
		t.Fatalf("media type = %q, want tv_series", found.Nodes[0].MediaType)
	}

	// Its season and episodes are where the tree says they are.
	work, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: result.WorkID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(work): %v", err)
	}
	if len(work.Children) != 1 || work.Children[0].Kind != v1.NodeContainer {
		t.Fatalf("work children = %+v, want one season", work.Children)
	}
	season, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: work.Children[0].ID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(season): %v", err)
	}
	if len(season.Children) != 2 {
		t.Fatalf("season has %d episodes, want 2", len(season.Children))
	}

	// Each episode has a RemoteLocation stream Part — the path ADR 0014 built
	// and nothing had exercised until now.
	for _, episode := range season.Children {
		parts, err := cs.Parts.ListByNode(c, episode.ID)
		if err != nil {
			t.Fatalf("ListByNode(%s): %v", episode.ID, err)
		}
		if len(parts) != 1 {
			t.Fatalf("episode %q has %d parts, want 1", episode.Title, len(parts))
		}
		loc := parts[0].Location
		if loc.Scheme != v1.RemoteLocation || loc.Provider != "stremio" || !strings.HasPrefix(loc.Ref, "http") {
			t.Fatalf("part location = %+v, want a remote stremio http ref", loc)
		}
	}

	// The invocation caused the content events: one work, three nodes
	// (season + two episodes), two parts, one binding.
	events, err := cs.Outbox.ListUnpublished(c, 100)
	if err != nil {
		t.Fatalf("ListUnpublished: %v", err)
	}
	seen := map[string]int{}
	for _, e := range events {
		seen[e.Type]++
	}
	if seen["content.work.created"] != 1 || seen["content.node.created"] != 3 || seen["content.part.attached"] != 2 || seen["content.source.bound"] != 1 {
		t.Fatalf("outbox event counts = %v, want work 1 / node 3 / part 2 / bound 1", seen)
	}

	// Re-invoking is idempotent: the source is already resolved.
	again, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller, Ref: v1.ContentRef{Provider: stremio.CapabilityID, NativeID: "tt0903747", NativeType: "series", MediaType: v1.MediaTVSeries, ExternalScheme: "imdb", ExternalID: "tt0903747"},
	})
	if err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}
	if !again.AlreadyKnown || again.WorkID != result.WorkID {
		t.Fatalf("second import should find the existing work, got %+v", again)
	}
}

// fakeStremioAddon serves a canned manifest, series meta and streams over HTTP.
func fakeStremioAddon() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case path == "/manifest.json":
			_, _ = w.Write([]byte(`{"id":"org.fake","name":"Fake","version":"1.0.0","resources":["meta","stream"],"types":["movie","series"]}`))
		case strings.HasPrefix(path, "/meta/series/"):
			_, _ = w.Write([]byte(`{"meta":{"id":"tt0903747","type":"series","name":"Breaking Bad","videos":[
				{"id":"tt0903747:1:1","title":"Pilot","season":1,"episode":1},
				{"id":"tt0903747:1:2","title":"Cat's in the Bag...","season":1,"episode":2}]}}`))
		case strings.HasPrefix(path, "/stream/"):
			_, _ = w.Write([]byte(`{"streams":[{"name":"Fake 1080p","url":"http://cdn.example/` + strings.TrimSuffix(path[len("/stream/"):], ".json") + `"}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
}
