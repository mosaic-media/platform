// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/mosaic-media/platform/capabilities/reference"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// animeMetadataJSON is what the fake provider serves — a work with a season,
// two episodes with files, and an adaptation pointing at its source manga.
const animeMetadataJSON = `{
  "provider": "anilist",
  "id": "5114",
  "title": "Fullmetal Alchemist: Brotherhood",
  "media_type": "anime_series",
  "external_ids": {"anilist": "5114", "mal": "5114"},
  "seasons": [
    {"title": "Season 1", "episodes": [
      {"title": "Fullmetal Alchemist", "file_path": "/media/fmab/s01e01.mkv", "duration_sec": 1440},
      {"title": "The First Day", "file_path": "/media/fmab/s01e02.mkv", "duration_sec": 1440}
    ]}
  ],
  "adaptation": {"provider": "anilist", "id": "30002", "title": "Fullmetal Alchemist", "media_type": "manga_series"}
}`

// TestReferenceCapabilityAgainstPostgres is the thesis test end to end: the
// reference capability, which imports only contracts/platform/v1, sources
// metadata over HTTP, searches to avoid duplicating, and creates a work, a
// season, episodes, parts, a source binding and an adaptation edge — all
// through the published ContentService, against a real database, acting as
// its invoking user (ADR 0012, 0016, 0017).
func TestReferenceCapabilityAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities:   nil, // the reference capability is invoked directly, not through the registry
		ModuleSettings: cs.ModuleSettings,
	})

	// A user with a session and the content actions the capability performs.
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
		domain.Permission(app.ActionContentCreate),
		domain.Permission(app.ActionContentRelate),
		domain.Permission(app.ActionContentBind),
		domain.Permission(app.ActionContentRead),
	}
	if err := seedRoleGrant(c, pool, user.ID, "Importer", actions); err != nil {
		t.Fatalf("seed role: %v", err)
	}

	// The provider the capability sources from. The capability owns this
	// integration entirely (ADR 0007); the Platform offers no HTTP contract.
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/work" || r.URL.Query().Get("q") == "" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(animeMetadataJSON))
	}))
	defer provider.Close()

	cap := reference.New(reference.NewHTTPSource(provider.URL))
	caller := v1.CallerFromSession(string(session.ID))

	// Run the import.
	result, err := cap.Import(c, svc, caller, "fullmetal alchemist brotherhood")
	if err != nil {
		t.Fatalf("Import: %v", err)
	}
	if result.WorkID == "" || result.AlreadyKnown {
		t.Fatalf("expected a freshly created work, got %+v", result)
	}
	if result.Containers != 1 || result.Items != 2 || result.Parts != 2 {
		t.Fatalf("tree shape = %d containers, %d items, %d parts; want 1/2/2", result.Containers, result.Items, result.Parts)
	}
	if result.Adaptation == "" {
		t.Fatal("expected an adaptation edge to the source manga")
	}

	// The work is findable through the published surface by its provider id.
	found, err := svc.FindContentByExternalID(c, v1.FindContentByExternalIDQuery{
		Caller: caller, Scheme: "anilist", Value: "5114",
	})
	if err != nil {
		t.Fatalf("FindContentByExternalID: %v", err)
	}
	if len(found.Nodes) != 1 || found.Nodes[0].ID != result.WorkID {
		t.Fatalf("lookup = %+v, want the imported work", found.Nodes)
	}

	// Its season and episodes are where the tree says they are.
	node, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: result.WorkID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode: %v", err)
	}
	if len(node.Children) != 1 || node.Children[0].Kind != v1.NodeContainer {
		t.Fatalf("work children = %+v, want one season", node.Children)
	}
	season, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: node.Children[0].ID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(season): %v", err)
	}
	if len(season.Children) != 2 {
		t.Fatalf("season has %d episodes, want 2", len(season.Children))
	}

	// The adaptation and both source bindings landed. Relations have no read
	// on the published surface yet, so this reads the store directly — a note
	// for the surface, not a defect in the capability.
	edges, err := cs.Relations.ListFrom(c, result.WorkID, v1.RelationAdaptation)
	if err != nil {
		t.Fatalf("ListFrom: %v", err)
	}
	if len(edges) != 1 || edges[0].ToNodeID != result.Adaptation {
		t.Fatalf("adaptation edges = %+v, want one to %q", edges, result.Adaptation)
	}

	// The capability caused events: every command it issued committed one.
	events, err := cs.Outbox.ListUnpublished(c, 100)
	if err != nil {
		t.Fatalf("ListUnpublished: %v", err)
	}
	seen := map[string]int{}
	for _, e := range events {
		seen[e.Type]++
	}
	for _, want := range []string{"content.work.created", "content.node.created", "content.part.attached", "content.source.bound", "content.relation.created"} {
		if seen[want] == 0 {
			t.Fatalf("expected outbox event %q; got %v", want, seen)
		}
	}
	// Two works (anime and its source manga) were created and bound.
	if seen["content.work.created"] != 2 || seen["content.source.bound"] != 2 {
		t.Fatalf("expected two works created and bound, got %v", seen)
	}

	// Running the same import again is idempotent: the source is already
	// resolved, so nothing new is created.
	again, err := cap.Import(c, svc, caller, "fullmetal alchemist brotherhood")
	if err != nil {
		t.Fatalf("second Import: %v", err)
	}
	if !again.AlreadyKnown || again.WorkID != result.WorkID {
		t.Fatalf("second import should find the existing work, got %+v", again)
	}
}
