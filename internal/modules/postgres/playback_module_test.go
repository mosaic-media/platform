// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	remoteplayback "github.com/mosaic-media/module-remote-playback"
	stremio "github.com/mosaic-media/module-stremio-addons"
	"github.com/mosaic-media/platform/internal/modules/postgres"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/transport/playback"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// TestPlaybackResolutionAgainstPostgres is the consumer half of the extension
// story end to end, and the point at which the library stops being inert.
//
// A source module imports a series and snapshots a stream location onto each
// episode; the Platform then reads that Part back out of a real database and
// hands it to a *different*, separately-compiled module — the first consumer —
// which resolves it to something playable. Two modules, neither importing the
// Platform, meeting only through the registry and the published SDK.
func TestPlaybackResolutionAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	addon := fakeStremioAddon()
	defer addon.Close()

	// Both modules registered exactly as the composition root does them: a
	// source and a consumer, side by side in one registry.
	registry := app.NewCapabilityRegistry()
	registry.Register(stremio.New(addon.Client()))
	registry.Register(remoteplayback.New())
	if err := registry.Verify(); err != nil {
		t.Fatalf("registry.Verify: %v", err)
	}

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities: registry, ModuleSettings: cs.ModuleSettings,
	})

	caller := seedPlaybackUser(t, c, cs, pool)

	if _, err := svc.ConfigureModule(c, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: stremio.CapabilityID,
		Settings: []byte(`{"addons":["` + addon.URL + `"],"disableDefaultAddons":true}`),
	}); err != nil {
		t.Fatalf("ConfigureModule: %v", err)
	}

	imported, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller,
		Ref: v1.ContentRef{
			Provider: stremio.CapabilityID, NativeID: "tt0903747", NativeType: "series",
			MediaType: v1.MediaTVSeries, ExternalScheme: "imdb", ExternalID: "tt0903747",
		},
	})
	if err != nil {
		t.Fatalf("ImportContent: %v", err)
	}

	// Walk to an episode's Part — the bytes the source snapshotted.
	work, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: imported.WorkID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(work): %v", err)
	}
	season, err := svc.GetContentNode(c, v1.GetContentNodeQuery{Caller: caller, NodeID: work.Children[0].ID, WithChildren: true})
	if err != nil {
		t.Fatalf("GetContentNode(season): %v", err)
	}
	parts, err := cs.Parts.ListByNode(c, season.Children[0].ID)
	if err != nil || len(parts) == 0 {
		t.Fatalf("ListByNode: %v (parts=%d)", err, len(parts))
	}
	part := parts[0]

	// The resolution itself: registry -> part read -> consumer module -> URL.
	res, err := svc.ResolvePlayback(c, app.ResolvePlaybackQuery{Caller: caller, PartID: part.ID})
	if err != nil {
		t.Fatalf("ResolvePlayback: %v", err)
	}
	if res.ModuleID != remoteplayback.CapabilityID {
		t.Errorf("resolved by %q, want %q", res.ModuleID, remoteplayback.CapabilityID)
	}
	if res.URL != part.Location.Ref {
		t.Errorf("URL = %q, want the part's location %q", res.URL, part.Location.Ref)
	}

	// And the resolution survives the round trip a client actually makes: the
	// origin seals it into a ticket, and the ticket must give the URL back
	// without ever having exposed it.
	sealer, err := playback.NewSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	ticket, err := sealer.Mint(res.URL, res.Headers, caller.Session, playback.Plan{DirectPlay: true})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if strings.Contains(ticket, addon.URL) {
		t.Error("the minted ticket leaked the upstream URL")
	}
}

// TestPlaybackResolutionWithNoConsumerInstalled pins ADR 0036's inert library:
// with only source modules registered, resolving playback must fail with a
// plain "nothing installed" rather than looking like a broken stream.
func TestPlaybackResolutionWithNoConsumerInstalled(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	// A source module only — no consumer.
	registry := app.NewCapabilityRegistry()
	registry.Register(stremio.New(nil))

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities: registry, ModuleSettings: cs.ModuleSettings,
	})

	caller := seedPlaybackUser(t, c, cs, pool)

	work, err := svc.AddContentWork(c, v1.AddContentWorkCommand{
		Caller: caller, Title: "Inert", MediaType: v1.MediaMovie,
	})
	if err != nil {
		t.Fatalf("AddContentWork: %v", err)
	}
	item, err := svc.AddContentChild(c, v1.AddContentChildCommand{
		Caller: caller, ParentID: work.Work.ID, Kind: v1.NodeItem, Title: "Inert", ItemType: v1.ItemFeature,
	})
	if err != nil {
		t.Fatalf("AddContentChild: %v", err)
	}
	attached, err := svc.AttachContentPart(c, v1.AttachContentPartCommand{
		Caller: caller, NodeID: item.Node.ID, Role: v1.PartEdition,
		Location: v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: "stremio", Ref: "https://cdn.example/a.mp4"},
	})
	if err != nil {
		t.Fatalf("AttachContentPart: %v", err)
	}

	_, err = svc.ResolvePlayback(c, app.ResolvePlaybackQuery{Caller: caller, PartID: attached.Part.ID})
	if err == nil {
		t.Fatal("ResolvePlayback succeeded with no playback module installed")
	}
	if !strings.Contains(err.Error(), "playback module") {
		t.Errorf("error %q does not say that nothing is installed", err)
	}
}

// seedPlaybackUser creates a user, session and role grant with the actions the
// import and the playback read both need.
func seedPlaybackUser(t *testing.T, c context.Context, cs *postgres.ContractSet, pool *pgxpool.Pool) v1.Caller {
	t.Helper()

	now := cs.Clock.Now()
	user, err := cs.Users.Create(c, domain.User{
		ID: "viewer", Username: "viewer", Email: "viewer@example.com",
		Status: domain.UserActive, CreatedAt: now, UpdatedAt: now,
	})
	if err != nil {
		t.Fatalf("seed user: %v", err)
	}
	session, err := cs.Sessions.Create(c, domain.Session{
		ID: "viewer-session", UserID: user.ID, DeviceID: "viewer-device",
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
	if err := seedRoleGrant(c, pool, user.ID, "Viewer", actions); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	return v1.CallerFromSession(string(session.ID))
}
