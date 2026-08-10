// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"
	"os/exec"
	"path/filepath"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/platform/app"
)

// These are the tests the in-package sdk/host ones could not be: a real child
// process, a real Unix socket, a real handshake. Everything below spawns
// test/extprobe.
//
// They build the probe rather than assuming a binary exists, so a green result
// means the module *source* in this repository works — not that someone
// remembered to compile it.

// buildProbe compiles test/extprobe and returns the binary path. It is built
// once per test binary run; go's build cache makes the repeat cost small.
func buildProbe(t *testing.T) string {
	t.Helper()

	bin := filepath.Join(t.TempDir(), "extprobe")
	cmd := exec.Command("go", "build", "-o", bin, "github.com/mosaic-media/platform/test/extprobe")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("building test/extprobe: %v\n%s", err, out)
	}
	return bin
}

// recordingContent is the Platform side of the callback. It is the only way to
// observe that the module reached back across the boundary at all.
type recordingContent struct {
	stubContentService

	gotCaller string
	gotTitle  string
	gotMedia  v1.MediaType
	calls     int
}

func (c *recordingContent) AddContentWork(_ context.Context, cmd v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	c.calls++
	c.gotCaller = cmd.Caller.Session
	c.gotTitle = cmd.Title
	c.gotMedia = cmd.MediaType
	return v1.AddContentWorkResult{Work: v1.Node{
		ID:        "work-42",
		WorkID:    "work-42",
		Kind:      v1.NodeWork,
		MediaType: cmd.MediaType,
		Title:     cmd.Title,
		Status:    v1.NodeActive,
	}}, nil
}

func launch(t *testing.T, content v1.ContentService) *extension.Module {
	t.Helper()

	m, err := extension.Launch(extension.Config{
		BinaryPath: buildProbe(t),
		Content:    content,
	})
	if err != nil {
		t.Fatalf("launch: %v", err)
	}
	t.Cleanup(m.Close)
	return m
}

// The whole mechanism in one test: spawn, handshake over a Unix socket, read
// the manifest back.
func TestLaunchSpawnsAModuleAndReadsItsManifest(t *testing.T) {
	m := launch(t, &recordingContent{})

	got := m.Capability.Manifest()
	if got.ID != "extprobe" {
		t.Errorf("manifest id: got %q, want extprobe", got.ID)
	}
	if got.Name != "Extension Probe" {
		t.Errorf("manifest name: got %q", got.Name)
	}
	want := []v1.Role{v1.RoleSearch, v1.RoleStream, v1.RoleSubtitles}
	if len(got.Provides) != len(want) {
		t.Fatalf("provides: got %v, want %v", got.Provides, want)
	}
	for i, r := range want {
		if got.Provides[i] != r {
			t.Errorf("provides[%d]: got %q, want %q", i, got.Provides[i], r)
		}
	}
}

// The callback direction, across a real process boundary: Import runs in the
// child and writes through the parent's ContentService, carrying the Caller
// handle it was given.
func TestImportCallsBackAcrossTheProcessBoundary(t *testing.T) {
	content := &recordingContent{}
	m := launch(t, content)

	out, err := m.Capability.Import(context.Background(), nil, v1.ImportRequest{
		Caller:   v1.CallerFromSession("invocation-handle-1"),
		Ref:      v1.ContentRef{Provider: "extprobe", NativeID: "tt0083658", MediaType: v1.MediaMovie},
		Settings: []byte(`{"probe":true}`),
	})
	if err != nil {
		t.Fatalf("import: %v", err)
	}

	if out.WorkID != "work-42" || out.Items != 1 {
		t.Errorf("result: got %+v, want WorkID=work-42 Items=1", out)
	}
	if content.calls != 1 {
		t.Fatalf("callbacks: got %d, want 1", content.calls)
	}
	if content.gotCaller != "invocation-handle-1" {
		t.Errorf("the Caller handle did not survive: got %q", content.gotCaller)
	}
	if content.gotTitle != "probe: tt0083658" {
		t.Errorf("callback payload: got title %q", content.gotTitle)
	}
	if content.gotMedia != v1.MediaMovie {
		t.Errorf("media type did not survive: got %q", content.gotMedia)
	}
}

func TestSearchRoleWorksAcrossTheProcessBoundary(t *testing.T) {
	m := launch(t, &recordingContent{})

	sp, ok := m.Capability.(v1.SearchProvider)
	if !ok {
		t.Fatal("the proxy is not a SearchProvider")
	}
	out, err := sp.Search(context.Background(), v1.SearchRequest{
		Caller: v1.CallerFromSession("h"),
		Text:   "blade runner",
		Limit:  5,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(out.Results) != 1 {
		t.Fatalf("results: got %d, want 1", len(out.Results))
	}
	if out.Results[0].Title != "probe result: blade runner" {
		t.Errorf("result title: got %q", out.Results[0].Title)
	}
	if out.Results[0].Ref.ExternalScheme != "probe" {
		t.Errorf("ref did not survive: got %+v", out.Results[0].Ref)
	}
}

// The registry must not be able to tell this from a compiled-in module, and it
// must resolve the probe's roles from the manifest — the probe fills three of
// the eight, and the proxy satisfies every provider interface.
func TestRegistryHoldsAnExtensionModuleLikeAnyOther(t *testing.T) {
	m := launch(t, &recordingContent{})

	reg := app.NewCapabilityRegistry()
	reg.Register(m.Capability)

	if _, ok := reg.Lookup("extprobe"); !ok {
		t.Fatal("the module did not register under its manifest id")
	}
	if err := reg.Verify(); err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if got := len(reg.SearchProviders()); got != 1 {
		t.Errorf("SearchProviders: got %d, want 1", got)
	}
	if got := len(reg.StreamProviders()); got != 1 {
		t.Errorf("StreamProviders: got %d, want 1", got)
	}
	// It declares no artwork or playback role, and the manifest is what the
	// registry reads — a bare type assertion against the proxy would have
	// returned it for both.
	if got := len(reg.ArtworkProviders()); got != 0 {
		t.Errorf("ArtworkProviders: got %d, want 0", got)
	}
	if got := len(reg.PlaybackProviders()); got != 0 {
		t.Errorf("PlaybackProviders: got %d, want 0", got)
	}
}

// TestStreamAndSubtitleFieldsCrossTheProcessBoundary is the strongest form of
// the claim SDK v0.26.0's five fields need: a real child process, a real Unix
// socket, a real handshake, real protobuf on the way there and back.
//
// sdk/host has its own version of this over a real gRPC connection, and it is
// not the same test. That one proves the converters and the dispatch with both
// ends in one process. This one proves the fields survive everything between
// two, which is the arrangement the failure has always hidden in: a field with
// no `module.proto` counterpart compiles on both sides, converts to nothing, and
// arrives as a zero — indistinguishable from a source that had nothing to say.
// It has happened three times.
//
// The two directions are asserted separately and deliberately. The three
// technical fields travel module-to-Platform and are read off what the caller
// receives. The two subtitle coordinates travel Platform-to-module, and the only
// witness is the child: it echoes them back in the track id, because nothing the
// caller can otherwise read reports what the child was handed.
func TestStreamAndSubtitleFieldsCrossTheProcessBoundary(t *testing.T) {
	m := launch(t, &recordingContent{})

	sp, ok := m.Capability.(v1.StreamProvider)
	if !ok {
		t.Fatal("the proxy is not a StreamProvider")
	}
	streams, err := sp.Streams(context.Background(), v1.StreamRequest{
		Caller: v1.CallerFromSession("h"),
		Ref:    v1.ContentRef{Provider: "extprobe", NativeID: "tt0083658", MediaType: v1.MediaMovie},
	})
	if err != nil {
		t.Fatalf("streams: %v", err)
	}
	if len(streams.Streams) != 1 {
		t.Fatalf("streams: got %d, want 1", len(streams.Streams))
	}
	link := streams.Streams[0]

	if link.Container != "mkv" {
		t.Errorf("StreamLink.Container did not cross the process boundary: got %q, want %q", link.Container, "mkv")
	}
	if link.VideoCodec != "hevc" {
		t.Errorf("StreamLink.VideoCodec did not cross the process boundary: got %q, want %q", link.VideoCodec, "hevc")
	}
	if link.AudioCodec != "eac3" {
		t.Errorf("StreamLink.AudioCodec did not cross the process boundary: got %q, want %q", link.AudioCodec, "eac3")
	}
	// The fields that already crossed, beside the new ones: three strings
	// appended to one message is the shape a converter garbles by pairing the
	// wrong field with the wrong value, and that reads as success unless
	// something pins the originals too.
	if link.Quality != "2160p" || link.Seeders != 99 || link.SizeBytes != 21_474_836_480 {
		t.Errorf("existing StreamLink fields did not survive beside the new ones: got %+v", link)
	}
	if link.Location.Scheme != v1.RemoteLocation || link.Location.Ref != "magnet:?xt=urn:btih:probe" {
		t.Errorf("Location did not survive: got %+v", link.Location)
	}

	sub, ok := m.Capability.(v1.SubtitlesProvider)
	if !ok {
		t.Fatal("the proxy is not a SubtitlesProvider")
	}
	subs, err := sub.Subtitles(context.Background(), v1.SubtitlesRequest{
		Caller:  v1.CallerFromSession("h"),
		Ref:     v1.ContentRef{ExternalScheme: "imdb", ExternalID: "tt0903747", MediaType: v1.MediaTVSeries},
		Season:  1,
		Episode: 2,
	})
	if err != nil {
		t.Fatalf("subtitles: %v", err)
	}
	if len(subs.Subtitles) != 1 {
		t.Fatalf("subtitles: got %d, want 1", len(subs.Subtitles))
	}
	// "s1e2" is the child's report of what arrived. "s0e0" is the failure this
	// exists to catch, and it is the one that does not look like a failure:
	// subtitles for the wrong episode are a track that does not match the
	// dialogue, not an error anyone sees.
	if got := subs.Subtitles[0].ID; got != "s1e2" {
		t.Errorf("SubtitlesRequest coordinates did not cross the process boundary: the child saw %q, want %q", got, "s1e2")
	}
}

// A role the module does not serve is refused rather than silently returning an
// empty result, so a Platform bug surfaces as an error.
func TestUnservedRoleIsRefusedAcrossTheBoundary(t *testing.T) {
	m := launch(t, &recordingContent{})

	ap, ok := m.Capability.(v1.ArtworkProvider)
	if !ok {
		t.Fatal("the proxy is not an ArtworkProvider")
	}
	if _, err := ap.Artwork(context.Background(), v1.ArtworkRequest{
		Caller: v1.CallerFromSession("h"),
	}); err == nil {
		t.Fatal("expected a refusal for a role the probe does not fill")
	}
}

// platform#39's requirement that the running binary agree with what the manifest
// file claimed. A mismatch refuses the connection rather than registering a
// module under an identity it does not have.
func TestManifestMismatchRefusesTheConnection(t *testing.T) {
	_, err := extension.Launch(extension.Config{
		BinaryPath:       buildProbe(t),
		Content:          &recordingContent{},
		DeclaredManifest: v1.Manifest{ID: "something-else"},
	})
	if err == nil {
		t.Fatal("a manifest id mismatch was accepted")
	}
}

func TestManifestDeclaringAnUnservedRoleRefusesTheConnection(t *testing.T) {
	_, err := extension.Launch(extension.Config{
		BinaryPath: buildProbe(t),
		Content:    &recordingContent{},
		DeclaredManifest: v1.Manifest{
			ID: "extprobe",
			// The probe serves search, stream and subtitles — not artwork.
			Provides: []v1.Role{v1.RoleSearch, v1.RoleArtwork},
		},
	})
	if err == nil {
		t.Fatal("a manifest declaring a role the binary does not serve was accepted")
	}
}

func TestMatchingManifestIsAccepted(t *testing.T) {
	m, err := extension.Launch(extension.Config{
		BinaryPath: buildProbe(t),
		Content:    &recordingContent{},
		DeclaredManifest: v1.Manifest{
			ID:       "extprobe",
			Version:  "v0.1.0",
			Provides: []v1.Role{v1.RoleSearch},
		},
	})
	if err != nil {
		t.Fatalf("a matching manifest was refused: %v", err)
	}
	t.Cleanup(m.Close)
}

func TestMissingBinaryIsRefused(t *testing.T) {
	if _, err := extension.Launch(extension.Config{
		BinaryPath: filepath.Join(t.TempDir(), "does-not-exist"),
		Content:    &recordingContent{},
	}); err == nil {
		t.Fatal("launching a nonexistent binary succeeded")
	}
}
