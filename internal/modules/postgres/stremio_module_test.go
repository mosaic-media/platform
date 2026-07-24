// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TestImportSourceModuleAgainstPostgres is the composition-and-invocation path
// end to end: the Platform invokes a registered capability through the
// ImportContent command, and the capability — handed the Service and the caller
// — lands a work, its source binding, a season/episode tree and RemoteLocation
// stream Parts in a real database, every write re-authorising as the invoking
// user (ADR 0007, 0008, 0017).
//
// The capability is a fake import source, not the real Stremio module: the
// platform module must not import an extension module (ADR 0079/0081). What is
// under test is the Platform's path — registry -> ImportContent -> capability ->
// ContentService — and the writes it makes on the module's behalf, not Stremio's
// addon parsing, which is the module's own test. That the *real* module works
// over this path is proven by the runtime-install integration surface.
//
// Unlike TestReferenceCapabilityAgainstPostgres, which calls a capability
// directly, this drives it through the registry and the ImportContent command.
func TestImportSourceModuleAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	// A source that materialises a series with one season of two episodes, each
	// with a remote stream Part.
	source := &fakeImportModule{
		id: "stremio",
		episodes: []fakeImportEpisode{
			{title: "Pilot", partRef: "http://cdn.example/tt0903747:1:1"},
			{title: "Cat's in the Bag...", partRef: "http://cdn.example/tt0903747:1:2"},
		},
	}

	// Register the capability exactly as the composition root registers a module,
	// then build the Service over that registry.
	registry := app.NewCapabilityRegistry()
	registry.Register(source)

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities: registry, ModuleSettings: cs.ModuleSettings,
	})

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

	// Invoke the capability through the Platform's generic import command. The
	// fake source reads no settings, so nothing is configured for it.
	result, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller, Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt0903747", NativeType: "series", MediaType: v1.MediaTVSeries, ExternalScheme: "imdb", ExternalID: "tt0903747"},
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
		Caller: caller, Ref: v1.ContentRef{Provider: "stremio", NativeID: "tt0903747", NativeType: "series", MediaType: v1.MediaTVSeries, ExternalScheme: "imdb", ExternalID: "tt0903747"},
	})
	if err != nil {
		t.Fatalf("second ImportContent: %v", err)
	}
	if !again.AlreadyKnown || again.WorkID != result.WorkID {
		t.Fatalf("second import should find the existing work, got %+v", again)
	}
}
