// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"fmt"
	"sort"

	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// CapabilityRegistry holds the optional-module capabilities the composition
// root registered, keyed by manifest id. The Platform routes an ImportContent
// command to one of them by id. It is populated once, at composition, and read
// at invocation — there is no runtime registration path (ADR 0007: modules are
// selected before the build, not discovered at runtime).
//
// It lives in the app package rather than under composition/ so the Service
// can hold it without an import cycle: it depends only on the published SDK,
// exactly as a module does.
type CapabilityRegistry struct {
	byID map[string]v1.Capability
}

// NewCapabilityRegistry returns an empty registry.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{byID: make(map[string]v1.Capability)}
}

// Register adds a capability under its manifest id. Registration order is the
// composition root's, and a repeated id replaces the earlier registration —
// the composition root controls both, so this stays a plain assignment rather
// than an error path.
func (r *CapabilityRegistry) Register(c v1.Capability) {
	r.byID[c.Manifest().ID] = c
}

// Lookup returns the capability registered under id, and whether one was.
func (r *CapabilityRegistry) Lookup(id string) (v1.Capability, bool) {
	c, ok := r.byID[id]
	return c, ok
}

// Manifests returns the manifest of every registered capability, so the
// composition root can report what it wired.
func (r *CapabilityRegistry) Manifests() []v1.Manifest {
	manifests := make([]v1.Manifest, 0, len(r.byID))
	for _, c := range r.byID {
		manifests = append(manifests, c.Manifest())
	}
	return manifests
}

// Verify checks that every capability implements the provider roles it declares
// in Manifest.Provides (ADR 0027). A role named but not backed by the matching
// interface is a composition error, caught here at boot rather than as a nil
// provider at invocation. The composition root calls this after registering.
func (r *CapabilityRegistry) Verify() error {
	for _, id := range r.sortedIDs() {
		c := r.byID[id]
		for _, role := range c.Manifest().Provides {
			if !roleImplemented(c, role) {
				return fmt.Errorf("capability %q declares role %q but does not implement its provider interface", id, role)
			}
		}
	}
	return nil
}

// roleImplemented reports whether c backs role with the matching provider
// interface. An unrecognised role is not rejected — a newer module may declare a
// role this Platform build does not know, and that is not this check's concern.
func roleImplemented(c v1.Capability, role v1.Role) bool {
	switch role {
	case v1.RoleMetadata:
		_, ok := c.(v1.MetadataProvider)
		return ok
	case v1.RoleSearch:
		_, ok := c.(v1.SearchProvider)
		return ok
	case v1.RoleCatalog:
		_, ok := c.(v1.CatalogProvider)
		return ok
	case v1.RoleStream:
		_, ok := c.(v1.StreamProvider)
		return ok
	case v1.RoleSubtitles:
		_, ok := c.(v1.SubtitlesProvider)
		return ok
	default:
		return true
	}
}

// SearchProviderEntry pairs a search-capable module's id with its provider, so a
// caller can read the module's settings before invoking it.
type SearchProviderEntry struct {
	ModuleID string
	Provider v1.SearchProvider
}

// SearchProviders returns every registered capability that fills RoleSearch, in
// stable module-id order so a fan-out's results do not depend on map iteration.
func (r *CapabilityRegistry) SearchProviders() []SearchProviderEntry {
	var out []SearchProviderEntry
	for _, id := range r.sortedIDs() {
		if p, ok := r.byID[id].(v1.SearchProvider); ok {
			out = append(out, SearchProviderEntry{ModuleID: id, Provider: p})
		}
	}
	return out
}

// CatalogProviderEntry pairs a catalog-capable module's id with its provider.
type CatalogProviderEntry struct {
	ModuleID string
	Provider v1.CatalogProvider
}

// CatalogProviders returns every registered capability that fills RoleCatalog,
// in stable module-id order.
func (r *CapabilityRegistry) CatalogProviders() []CatalogProviderEntry {
	var out []CatalogProviderEntry
	for _, id := range r.sortedIDs() {
		if p, ok := r.byID[id].(v1.CatalogProvider); ok {
			out = append(out, CatalogProviderEntry{ModuleID: id, Provider: p})
		}
	}
	return out
}

// CatalogProvider returns the catalog provider registered under id, if that
// capability fills RoleCatalog.
func (r *CapabilityRegistry) CatalogProvider(id string) (v1.CatalogProvider, bool) {
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	p, ok := c.(v1.CatalogProvider)
	return p, ok
}

// MetadataProvider returns the metadata provider registered under id, if that
// capability fills RoleMetadata.
func (r *CapabilityRegistry) MetadataProvider(id string) (v1.MetadataProvider, bool) {
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	p, ok := c.(v1.MetadataProvider)
	return p, ok
}

// SubtitlesProvider returns the subtitles provider registered under id, if that
// capability fills RoleSubtitles (ADR 0037). The consumer is a future player; the
// resolver exists so that consumer has a seam to reach it, like the others.
func (r *CapabilityRegistry) SubtitlesProvider(id string) (v1.SubtitlesProvider, bool) {
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	p, ok := c.(v1.SubtitlesProvider)
	return p, ok
}

// sortedIDs returns the registered ids in lexical order, the stable order every
// enumeration uses.
func (r *CapabilityRegistry) sortedIDs() []string {
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
