// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"fmt"
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

// episodeStreamFor returns a stream-source behaviour that answers for the IMDB
// identity by composing a Stremio-style episode id from the shared id and the
// season and episode the Platform passes on the request. It is what lets a fake
// stand in for a real stream source while still proving the Platform handed it
// the coordinates (ADR 0073) rather than the fake inventing them.
func episodeStreamFor(provider string) func(v1.StreamRequest) []v1.StreamLink {
	return func(req v1.StreamRequest) []v1.StreamLink {
		if req.Ref.ExternalScheme != "imdb" {
			return nil
		}
		return []v1.StreamLink{{
			Location: v1.MediaLocation{
				Scheme:   v1.RemoteLocation,
				Provider: provider,
				Ref:      fmt.Sprintf("http://cdn.example/%s:%d:%d", req.Ref.ExternalID, req.Season, req.Episode),
			},
		}}
	}
}

// TestStreamProviderPrecedenceAgainstPostgres checks the precedence claim in
// `registerCapabilities` — that of two stream sources able to answer, the one
// whose module id sorts first is asked first and wins (ADR 0073) — with the real
// enrichment pass and a real database.
//
// Both sources are fakes: the platform module must not import an extension module
// (ADR 0079/0081), and what is under test is the Platform's fan-out order, not a
// source's addon parsing. Two sources are registered deliberately — the ordering
// rule is otherwise a comment nothing checks — and both can answer for the shared
// IMDB identity, so which one's Part lands is decided by id order alone.
func TestStreamProviderPrecedenceAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	// The metadata source: a series with one season of two episodes, carrying the
	// IMDB id that is the only identity these sources share.
	metadata := fakeTMDBForEnrichment()
	defer metadata.Close()

	// Two stream sources that both answer. "aiostreams" sorts before "stremio",
	// so the precedence rule must pick it.
	registry := app.NewCapabilityRegistry()
	registry.Register(tmdb.New(redirectTo(metadata)))
	registry.Register(&fakeStreamModule{id: "aiostreams", streamsFor: episodeStreamFor("aiostreams")})
	registry.Register(&fakeStreamModule{id: "stremio", streamsFor: episodeStreamFor("stremio")})
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
	// The stream sources read no settings, so nothing is configured for them.

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
		t.Fatal("no parts attached: a registered stream source never contributed")
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
		// stream sources can answer, and module-id order decides — "aiostreams"
		// sorts before "stremio".
		if part.Location.Provider != "aiostreams" {
			t.Errorf("episode %q resolved through %q, want aiostreams — module-id order is the precedence rule",
				episode.Title, part.Location.Provider)
		}
		// The Platform never built a Stremio-style episode id. The source composed
		// it from the shared IMDB id plus the season and episode numbers the
		// Platform read off the tree — which is the whole point of the coordinates
		// on StreamRequest.
		if !strings.Contains(part.Location.Ref, "tt0903747:1:") {
			t.Errorf("episode %q ref = %q, want an id composed from the shared IMDB identity",
				episode.Title, part.Location.Ref)
		}
	}
}

// TestStreamSourceDeclinesButImportSucceeds is the state a fresh install is in,
// all the way through the Platform: a stream source is registered but has nothing
// to offer, so it declines.
//
// The assertion is that the import *succeeds* — a stream source with nothing to
// offer must not lose a user the title they just added. It is the same rule as a
// metadata-only deployment, arriving by a different route.
func TestStreamSourceDeclinesButImportSucceeds(t *testing.T) {
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

	// A stream source that declines everything — an instance with no profile
	// behind it, modelled as a source that answers nothing.
	registry := app.NewCapabilityRegistry()
	registry.Register(tmdb.New(redirectTo(metadata)))
	registry.Register(&fakeStreamModule{
		id:         "aiostreams",
		streamsFor: func(v1.StreamRequest) []v1.StreamLink { return nil },
	})
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
	// The declining stream source reads no settings.

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
		t.Fatalf("attached %d parts from a source that declares nothing", result.Parts)
	}
}
