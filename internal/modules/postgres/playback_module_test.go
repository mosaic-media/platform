// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	remoteplayback "github.com/mosaic-media/module-remote-playback"
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
// A source imports a series and snapshots a stream location onto each episode;
// the Platform then reads that Part back out of a real database and hands it to a
// *different*, separately-compiled module — the first consumer, remote playback —
// which resolves it to something playable. The consumer is the real core module;
// the source is a fake, because the platform module must not import an extension
// module (ADR 0079/0081) and what is under test is the Platform's read-back and
// consumer hand-off, not the source's addon parsing.
func TestPlaybackResolutionAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	const streamURL = "https://cdn.example/tt0903747-s01e01.mp4"

	// A source and the real consumer, side by side in one registry as the
	// composition root registers them.
	registry := app.NewCapabilityRegistry()
	registry.Register(&fakeImportModule{
		id: "stremio",
		episodes: []fakeImportEpisode{
			{title: "Pilot", partRef: streamURL},
			{title: "Cat's in the Bag...", partRef: "https://cdn.example/tt0903747-s01e02.mp4"},
		},
	})
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

	imported, err := svc.ImportContent(c, app.ImportContentCommand{
		Caller: caller,
		Ref: v1.ContentRef{
			Provider: "stremio", NativeID: "tt0903747", NativeType: "series",
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
	if strings.Contains(ticket, "cdn.example") {
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

	// A source only — no consumer.
	registry := app.NewCapabilityRegistry()
	registry.Register(&fakeImportModule{id: "stremio"})

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
		domain.Permission(app.ActionPlaybackRead),
		domain.Permission(app.ActionPlaybackWrite),
		domain.Permission(app.ActionModuleConfigure),
	}
	if err := seedRoleGrant(c, pool, user.ID, "Viewer", actions); err != nil {
		t.Fatalf("seed role: %v", err)
	}
	return v1.CallerFromSession(string(session.ID))
}

// TestPlaybackResolutionCacheAgainstPostgres proves the durable/perishable split
// does what it is for (ADR 0049): the second play of the same release, for a
// client of the same capability class, does not ask the source at all.
//
// The proof is the absence of the module rather than a timing measurement. A
// second Service is built over the same database with **no** playback capability
// registered — the exact configuration the test above shows failing with
// "no playback module is installed" — and asked to resolve the same part. If it
// answers, the answer can only have come from the cache.
func TestPlaybackResolutionCacheAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	registry := app.NewCapabilityRegistry()
	registry.Register(remoteplayback.New())

	deps := func(reg *app.CapabilityRegistry) app.Deps {
		return app.Deps{
			UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
			Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts, Clock: cs.Clock,
			IDs: cs.IDs, ContentIDs: cs.ContentIDs,
			Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
			Capabilities: reg, ModuleSettings: cs.ModuleSettings,
			PlaybackResolutions: cs.PlaybackResolutions,
		}
	}
	svc := app.NewService(deps(registry))
	caller := seedPlaybackUser(t, c, cs, pool)

	work, err := svc.AddContentWork(c, v1.AddContentWorkCommand{
		Caller: caller, Title: "Cached", MediaType: v1.MediaMovie,
	})
	if err != nil {
		t.Fatalf("AddContentWork: %v", err)
	}
	item, err := svc.AddContentChild(c, v1.AddContentChildCommand{
		Caller: caller, ParentID: work.Work.ID, Kind: v1.NodeItem, Title: "Cached", ItemType: v1.ItemFeature,
	})
	if err != nil {
		t.Fatalf("AddContentChild: %v", err)
	}
	attached, err := svc.AttachContentPart(c, v1.AttachContentPartCommand{
		Caller: caller, NodeID: item.Node.ID, Role: v1.PartEdition,
		Location: v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: "stremio", Ref: "https://cdn.example/cached.mp4"},
	})
	if err != nil {
		t.Fatalf("AttachContentPart: %v", err)
	}

	const class = "class-browser"

	// Cold: the module resolves, and the answer is written to the cache.
	cold, err := svc.ResolvePlayback(c, app.ResolvePlaybackQuery{
		Caller: caller, PartID: attached.Part.ID, CapabilityClass: class,
	})
	if err != nil {
		t.Fatalf("cold ResolvePlayback: %v", err)
	}
	if cold.Cached {
		t.Error("the first resolution reported itself as cached")
	}
	if cold.ModuleID != remoteplayback.CapabilityID {
		t.Errorf("cold resolution came from %q, want the playback module", cold.ModuleID)
	}

	// Warm, and deliberately crippled: no playback capability at all.
	starved := app.NewService(deps(app.NewCapabilityRegistry()))
	warm, err := starved.ResolvePlayback(c, app.ResolvePlaybackQuery{
		Caller: caller, PartID: attached.Part.ID, CapabilityClass: class,
	})
	if err != nil {
		t.Fatalf("warm ResolvePlayback with no module installed: %v", err)
	}
	if !warm.Cached {
		t.Error("the second resolution did not report itself as cached")
	}
	if warm.URL != cold.URL {
		t.Errorf("cached URL = %q, want %q", warm.URL, cold.URL)
	}

	// A different class is a different key, and must miss. Sharing one entry
	// across classes is the bug the key exists to prevent — it is how a phone
	// ends up served the answer chosen for a television.
	if _, err := starved.ResolvePlayback(c, app.ResolvePlaybackQuery{
		Caller: caller, PartID: attached.Part.ID, CapabilityClass: "class-television",
	}); err == nil {
		t.Fatal("a different capability class read another class's cache entry")
	}

	// And no class at all disables the cache rather than sharing one bucket.
	if _, err := starved.ResolvePlayback(c, app.ResolvePlaybackQuery{
		Caller: caller, PartID: attached.Part.ID,
	}); err == nil {
		t.Fatal("an unclassed client read a cache entry")
	}
}

// TestPartProbeIsDurableAgainstPostgres proves the second play of a release does
// not re-derive what the first one learned (ADR 0050).
//
// The probe is what remained expensive once ADR 0049's cache removed the
// aggregator call, and it is pure waste: a probe describes bytes, and the bytes
// do not change. This walks the storage path without ffmpeg — recording a probe
// as the transport would, then reading it back through ResolvePlayback exactly
// as the play path does.
func TestPartProbeIsDurableAgainstPostgres(t *testing.T) {
	requirePostgres(t)

	pool := freshDatabase(t)
	c := context.Background()
	if err := postgres.Migrate(c, pool); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	var mod postgres.Module
	cs := mod.Bind(pool)

	registry := app.NewCapabilityRegistry()
	registry.Register(remoteplayback.New())

	svc := app.NewService(app.Deps{
		UnitOfWork: cs.UnitOfWork, Sessions: cs.Sessions, Users: cs.Users, Credentials: cs.Credentials,
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities: registry, ModuleSettings: cs.ModuleSettings,
		PlaybackResolutions: cs.PlaybackResolutions,
	})
	caller := seedPlaybackUser(t, c, cs, pool)

	work, err := svc.AddContentWork(c, v1.AddContentWorkCommand{
		Caller: caller, Title: "Probed", MediaType: v1.MediaMovie,
	})
	if err != nil {
		t.Fatalf("AddContentWork: %v", err)
	}
	item, err := svc.AddContentChild(c, v1.AddContentChildCommand{
		Caller: caller, ParentID: work.Work.ID, Kind: v1.NodeItem, Title: "Probed", ItemType: v1.ItemFeature,
	})
	if err != nil {
		t.Fatalf("AddContentChild: %v", err)
	}
	// Attached with what a release *name* suggested, which is what the module's
	// dialect table produces and what ADR 0050 demoted to a ranking hint. Both
	// of these are about to be contradicted by the bytes.
	attached, err := svc.AttachContentPart(c, v1.AttachContentPartCommand{
		Caller: caller, NodeID: item.Node.ID, Role: v1.PartEdition,
		Location:   v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: "stremio", Ref: "https://cdn.example/probed.mkv"},
		Container:  "mp4",
		VideoCodec: "h264",
		HDRFormat:  "hdr10",
		Attributes: []byte(`{"source":{"addon":"aio"}}`),
	})
	if err != nil {
		t.Fatalf("AttachContentPart: %v", err)
	}

	// Nothing stored yet: the play path must find no probe and fall through to
	// running one.
	cold, err := svc.ResolvePlayback(c, app.ResolvePlaybackQuery{
		Caller: caller, PartID: attached.Part.ID, CapabilityClass: "class-browser",
	})
	if err != nil {
		t.Fatalf("cold ResolvePlayback: %v", err)
	}
	if len(cold.Probe) != 0 {
		t.Errorf("an unprobed part reported a probe: %s", cold.Probe)
	}

	info := playback.MediaInfo{
		Container: "matroska",
		SizeBytes: 34_336_638_566,
		Video: playback.VideoTrack{
			Index: 0, Codec: "hevc", Width: 3840, Height: 2160, Profile: "Main 10",
		},
		Audio: []playback.AudioTrack{
			{Index: 1, Codec: "eac3", Channels: 6, Language: "hin", Default: true},
			{Index: 2, Codec: "aac", Channels: 2, Language: "eng"},
		},
	}
	doc, err := playback.Encode(info)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	recorded, err := svc.RecordPartProbe(c, app.RecordPartProbeCommand{
		Caller: caller, PartID: attached.Part.ID,
		Container: info.Container, VideoCodec: info.Video.Codec,
		AudioCodec: playback.SummaryAudioCodec(info),
		Width:      info.Video.Width, Height: info.Video.Height,
		HDRFormat: info.Video.HDRFormat, SizeBytes: info.SizeBytes,
		Probe: doc,
	})
	if err != nil {
		t.Fatalf("RecordPartProbe: %v", err)
	}

	// The bytes overrule the name. Both of these were wrong on the way in.
	if recorded.Part.Container != "matroska" {
		t.Errorf("container = %q, want matroska: the probe did not overrule the parsed name", recorded.Part.Container)
	}
	if recorded.Part.VideoCodec != "hevc" {
		t.Errorf("video codec = %q, want hevc", recorded.Part.VideoCodec)
	}
	// HDR especially: the name said hdr10 and the bytes say nothing, so the
	// guess has to be cleared or this release is tone-mapped forever.
	if recorded.Part.HDRFormat != "" {
		t.Errorf("HDR format = %q, want empty: a name's HDR guess outlived the probe", recorded.Part.HDRFormat)
	}
	// The summary names the track that would play, not the default-flagged one.
	if recorded.Part.AudioCodec != "aac" {
		t.Errorf("audio codec = %q, want aac", recorded.Part.AudioCodec)
	}

	// A pre-existing attribute from another writer must survive.
	var attrs map[string]json.RawMessage
	if err := json.Unmarshal(recorded.Part.Attributes, &attrs); err != nil {
		t.Fatalf("attributes are not an object: %v", err)
	}
	if _, ok := attrs["source"]; !ok {
		t.Error("recording a probe erased another writer's attribute")
	}
	if _, ok := attrs[app.ProbeAttribute]; !ok {
		t.Fatal("the probe document was not stored")
	}

	// And the play path reads it back, unchanged, into the same plan.
	warm, err := svc.ResolvePlayback(c, app.ResolvePlaybackQuery{
		Caller: caller, PartID: attached.Part.ID, CapabilityClass: "class-browser",
	})
	if err != nil {
		t.Fatalf("warm ResolvePlayback: %v", err)
	}
	stored, ok := playback.Decode(warm.Probe)
	if !ok {
		t.Fatal("the stored probe did not decode on the way back out")
	}
	if !reflect.DeepEqual(stored, info) {
		t.Errorf("probe changed in storage:\n got %+v\nwant %+v", stored, info)
	}
}

// TestPlaybackStateAgainstPostgres exercises the per-user store against real SQL
// (ADR 0046).
//
// The fake in the app package encodes the same rules in Go, which is exactly why
// this is worth having: the two can agree with each other and both be wrong
// about what the database does. The filters here — NotFound for an item never
// started, and a continue-watching list that excludes finished items and items
// opened at zero — live in the SQL and the partial index, not in Go.
func TestPlaybackStateAgainstPostgres(t *testing.T) {
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
		Config: cs.Config, Permissions: cs.Permissions, Nodes: cs.Nodes, Parts: cs.Parts, Clock: cs.Clock,
		IDs: cs.IDs, ContentIDs: cs.ContentIDs,
		Policy: policy.NewEngine(cs.Permissions), Events: noopPublisher{}, PasswordVerifier: reversibleVerifier{},
		Capabilities: app.NewCapabilityRegistry(), ModuleSettings: cs.ModuleSettings,
		PlaybackStates: cs.PlaybackStates,
	})
	caller := seedPlaybackUser(t, c, cs, pool)

	// Three items: one part-way through, one finished, one opened and abandoned
	// at zero. Only the first belongs in a continue-watching rail.
	items := map[string]v1.NodeID{}
	for _, title := range []string{"Watching", "Finished", "Opened"} {
		work, err := svc.AddContentWork(c, v1.AddContentWorkCommand{
			Caller: caller, Title: title, MediaType: v1.MediaMovie,
		})
		if err != nil {
			t.Fatalf("AddContentWork(%s): %v", title, err)
		}
		item, err := svc.AddContentChild(c, v1.AddContentChildCommand{
			Caller: caller, ParentID: work.Work.ID, Kind: v1.NodeItem, Title: title, ItemType: v1.ItemFeature,
		})
		if err != nil {
			t.Fatalf("AddContentChild(%s): %v", title, err)
		}
		items[title] = item.Node.ID
	}

	// Never started reads as absent rather than as a zero position.
	if res, err := svc.GetPlaybackState(c, v1.GetPlaybackStateQuery{
		Caller: caller, NodeID: items["Watching"],
	}); err != nil {
		t.Fatalf("GetPlaybackState: %v", err)
	} else if res.Found {
		t.Error("an item nobody has started reported a state")
	}

	const runtime = 100 * time.Minute
	progress := func(node v1.NodeID, position time.Duration) v1.PlaybackState {
		t.Helper()
		res, err := svc.RecordPlaybackProgress(c, v1.RecordPlaybackProgressCommand{
			Caller: caller, NodeID: node, Position: position, Duration: runtime,
		})
		if err != nil {
			t.Fatalf("RecordPlaybackProgress: %v", err)
		}
		return res.State
	}

	watching := progress(items["Watching"], 40*time.Minute)
	finished := progress(items["Finished"], 99*time.Minute)
	progress(items["Opened"], 0)

	if watching.Finished {
		t.Error("40 of 100 minutes was marked finished")
	}
	if !finished.Finished {
		t.Error("99 of 100 minutes was not marked finished")
	}

	// Round-tripping the duration through bigint milliseconds must not drift.
	if back, err := svc.GetPlaybackState(c, v1.GetPlaybackStateQuery{
		Caller: caller, NodeID: items["Watching"],
	}); err != nil {
		t.Fatalf("GetPlaybackState: %v", err)
	} else if back.State.Position != 40*time.Minute || back.State.Duration != runtime {
		t.Errorf("position/duration came back as %v/%v", back.State.Position, back.State.Duration)
	}

	// The continue-watching query, and the two exclusions that live in its SQL.
	list, err := svc.ListInProgress(c, v1.ListInProgressQuery{Caller: caller})
	if err != nil {
		t.Fatalf("ListInProgress: %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("in-progress list has %d items, want 1: %+v", len(list.Items), list.Items)
	}
	if list.Items[0].State.NodeID != items["Watching"] {
		t.Errorf("in-progress list contains %q", list.Items[0].Node.Title)
	}
	// And the Node travels with it, so a rail can render without a second query.
	if list.Items[0].Node.Title != "Watching" {
		t.Errorf("in-progress item carries node %q, want the item it belongs to", list.Items[0].Node.Title)
	}

	// The batch read a season of episodes uses: present for what exists, absent
	// for what does not, in one query.
	states, err := svc.ListPlaybackStates(c, v1.ListPlaybackStatesQuery{
		Caller:  caller,
		NodeIDs: []v1.NodeID{items["Watching"], items["Finished"], "00000000-0000-7000-8000-000000000000"},
	})
	if err != nil {
		t.Fatalf("ListPlaybackStates: %v", err)
	}
	if len(states.States) != 2 {
		t.Errorf("batch read returned %d states, want 2", len(states.States))
	}
	if _, ok := states.States[items["Opened"]]; ok {
		t.Error("an item at position zero has no state to return, but one came back")
	}
}
