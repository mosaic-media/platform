// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/platform/app"
)

// everythingCapability implements every provider interface while declaring
// whatever its manifest says — which is exactly the shape of an out-of-process
// module's proxy (ADR 0064).
//
// The proxy cannot be selective: the registry must not be able to tell it from
// a local struct, and Go type assertions cannot be made conditional at runtime,
// so it satisfies all eight interfaces unconditionally. Every resolution in the
// registry therefore has to gate on the *manifest*, and this double is what
// makes that testable without spawning a process.
type everythingCapability struct {
	manifest v1.Manifest
}

func (c *everythingCapability) Manifest() v1.Manifest { return c.manifest }

func (c *everythingCapability) Import(context.Context, v1.ContentService, v1.ImportRequest) (v1.ImportResult, error) {
	return v1.ImportResult{}, nil
}
func (c *everythingCapability) Metadata(context.Context, v1.MetadataRequest) (v1.ContentMetadata, error) {
	return v1.ContentMetadata{}, nil
}
func (c *everythingCapability) Search(context.Context, v1.SearchRequest) (v1.SearchResponse, error) {
	return v1.SearchResponse{}, nil
}
func (c *everythingCapability) Catalogs(context.Context, v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	return v1.CatalogsResponse{}, nil
}
func (c *everythingCapability) CatalogItems(context.Context, v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	return v1.CatalogItemsResponse{}, nil
}
func (c *everythingCapability) Streams(context.Context, v1.StreamRequest) (v1.StreamResponse, error) {
	return v1.StreamResponse{}, nil
}
func (c *everythingCapability) Subtitles(context.Context, v1.SubtitlesRequest) (v1.SubtitlesResponse, error) {
	return v1.SubtitlesResponse{}, nil
}
func (c *everythingCapability) Artwork(context.Context, v1.ArtworkRequest) (v1.ArtworkResponse, error) {
	return v1.ArtworkResponse{}, nil
}
func (c *everythingCapability) Resolve(context.Context, v1.PlaybackRequest) (v1.PlaybackResolution, error) {
	return v1.PlaybackResolution{}, nil
}
func (c *everythingCapability) SettingsUI(context.Context, v1.SettingsUIRequest) (v1.SettingsUIResponse, error) {
	return v1.SettingsUIResponse{}, nil
}

// A metadata-only module reached through a proxy must not be enumerated as a
// stream, artwork, catalog or playback provider. Before the registry gated on
// the manifest, every one of these returned it — a bare type assertion against
// a proxy always succeeds.
func TestProxyIsResolvedByManifestNotTypeAssertion(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "metadata-only",
		Provides: []v1.Role{v1.RoleMetadata},
	}})

	if got := len(reg.StreamProviders()); got != 0 {
		t.Errorf("StreamProviders: got %d, want 0 — a metadata-only module was enumerated as a stream source", got)
	}
	if got := len(reg.ArtworkProviders()); got != 0 {
		t.Errorf("ArtworkProviders: got %d, want 0", got)
	}
	if got := len(reg.CatalogProviders()); got != 0 {
		t.Errorf("CatalogProviders: got %d, want 0", got)
	}
	if got := len(reg.PlaybackProviders()); got != 0 {
		t.Errorf("PlaybackProviders: got %d, want 0", got)
	}
	if got := len(reg.SearchProviders()); got != 0 {
		t.Errorf("SearchProviders: got %d, want 0", got)
	}
	if got := len(reg.SettingsUIProviders()); got != 0 {
		t.Errorf("SettingsUIProviders: got %d, want 0", got)
	}

	// The one role it does declare resolves.
	if _, ok := reg.MetadataProvider("metadata-only"); !ok {
		t.Error("MetadataProvider: the declared role did not resolve")
	}
}

// The single-id resolvers have the same requirement as the enumerations.
func TestSingleResolversGateOnTheManifest(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "metadata-only",
		Provides: []v1.Role{v1.RoleMetadata},
	}})

	if _, ok := reg.PlaybackProvider("metadata-only"); ok {
		t.Error("PlaybackProvider resolved a role the manifest does not declare")
	}
	if _, ok := reg.CatalogProvider("metadata-only"); ok {
		t.Error("CatalogProvider resolved a role the manifest does not declare")
	}
	if _, ok := reg.SubtitlesProvider("metadata-only"); ok {
		t.Error("SubtitlesProvider resolved a role the manifest does not declare")
	}
	if _, ok := reg.SettingsUIProvider("metadata-only"); ok {
		t.Error("SettingsUIProvider resolved a role the manifest does not declare")
	}
}

// The worst case the manifest gate prevents: a module that fills nothing
// satisfying the check that a Mosaic can identify and find content (ADR 0035).
func TestRequireRolesIsNotSatisfiedByAProxyThatDeclaresNothing(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{ID: "declares-nothing"}})

	if err := reg.RequireRoles(v1.RoleMetadata, v1.RoleSearch); err == nil {
		t.Fatal("RequireRoles was satisfied by a capability declaring no roles at all")
	}
}

func TestRequireRolesPassesWhenTheRolesAreDeclared(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "full",
		Provides: []v1.Role{v1.RoleMetadata, v1.RoleSearch},
	}})

	if err := reg.RequireRoles(v1.RoleMetadata, v1.RoleSearch); err != nil {
		t.Fatalf("RequireRoles: %v", err)
	}
}

// Unregister makes a module unresolvable across every seam — the runtime
// uninstall path (ADR 0081): after it, nothing routes to a process being torn
// down. A bare Lookup, a role resolution, and the enumerations all stop finding
// it, and RequireRoles goes back to failing for the role it filled.
func TestUnregisterMakesACapabilityUnresolvable(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "stremio",
		Provides: []v1.Role{v1.RoleMetadata, v1.RoleSearch},
	}})

	if _, ok := reg.Lookup("stremio"); !ok {
		t.Fatal("precondition: the module should be registered")
	}
	if err := reg.RequireRoles(v1.RoleSearch); err != nil {
		t.Fatalf("precondition: search should be filled: %v", err)
	}

	reg.Unregister("stremio")

	if _, ok := reg.Lookup("stremio"); ok {
		t.Error("Lookup still finds an unregistered module")
	}
	if _, ok := reg.MetadataProvider("stremio"); ok {
		t.Error("MetadataProvider still resolves an unregistered module")
	}
	if got := reg.SearchProviders(); len(got) != 0 {
		t.Errorf("SearchProviders still enumerates an unregistered module: %+v", got)
	}
	if got := reg.Manifests(); len(got) != 0 {
		t.Errorf("Manifests still lists an unregistered module: %+v", got)
	}
	// The role it filled is unfilled again — the composition check would refuse a
	// Mosaic that had only this module and then uninstalled it.
	if err := reg.RequireRoles(v1.RoleSearch); err == nil {
		t.Error("RequireRoles is still satisfied after the only search module was unregistered")
	}

	// Unregistering an absent id is a no-op, not a panic — a retried uninstall.
	reg.Unregister("stremio")
	reg.Unregister("never-existed")
}

// The registry is read while it is being mutated — an install or uninstall
// happens while requests resolve capabilities. Register, Unregister and the read
// paths run concurrently here; under -race this is what proves the RWMutex
// actually guards the map rather than the single-threaded boot path having hidden
// the absence of one.
func TestConcurrentRegisterUnregisterAndRead(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	// A stable core module always present, so readers always have something to
	// find and the concurrent churn is only over the extension.
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID: "core", Provides: []v1.Role{v1.RoleMetadata},
	}})

	ext := &everythingCapability{manifest: v1.Manifest{
		ID: "ext", Provides: []v1.Role{v1.RoleSearch, v1.RoleStream},
	}}

	const workers = 8
	const iterations = 500
	done := make(chan struct{})
	for w := 0; w < workers; w++ {
		go func(w int) {
			defer func() { done <- struct{}{} }()
			for i := 0; i < iterations; i++ {
				switch (w + i) % 5 {
				case 0:
					reg.Register(ext)
				case 1:
					reg.Unregister("ext")
				case 2:
					reg.SearchProviders()
				case 3:
					_, _ = reg.Lookup("core")
				case 4:
					_ = reg.Manifests()
				}
			}
		}(w)
	}
	for w := 0; w < workers; w++ {
		<-done
	}

	// The core module is untouched by the churn and must still be there.
	if _, ok := reg.Lookup("core"); !ok {
		t.Fatal("the stable core module went missing during concurrent churn")
	}
}
