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
