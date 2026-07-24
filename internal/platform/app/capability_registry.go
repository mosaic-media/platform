// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"fmt"
	"sort"
	"sync"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// CapabilityRegistry holds the optional-module capabilities the Platform can
// route to, keyed by manifest id. The Platform routes an ImportContent command
// to one of them by id.
//
// Core modules are registered once, at composition. Extension modules are
// registered and unregistered *while the Platform serves* — a user installs or
// uninstalls one at runtime (ADR 0081) — so the map is guarded by a RWMutex: a
// resolution during serving takes the read lock, an install or uninstall the
// write lock. It was a bare map until extensions became runtime-managed, which
// was correct only while every registration happened before the first request.
//
// It lives in the app package rather than under composition/ so the Service
// can hold it without an import cycle: it depends only on the published SDK,
// exactly as a module does.
type CapabilityRegistry struct {
	mu   sync.RWMutex
	byID map[string]v1.Capability
}

// NewCapabilityRegistry returns an empty registry.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{byID: make(map[string]v1.Capability)}
}

// Register adds a capability under its manifest id, replacing any earlier one
// under the same id — a reinstall swaps the running proxy for a fresh one. Safe
// to call while the Platform serves: a runtime install registers here.
func (r *CapabilityRegistry) Register(c v1.Capability) {
	id := c.Manifest().ID
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[id] = c
}

// Unregister removes the capability under id, if any. It is how a runtime
// uninstall (ADR 0081) makes a module unresolvable: after it returns, no lookup
// or role enumeration finds the module, so nothing routes to a process that is
// being torn down. Removing an id that is not present is a no-op.
func (r *CapabilityRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
}

// Lookup returns the capability registered under id, and whether one was.
func (r *CapabilityRegistry) Lookup(id string) (v1.Capability, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	return c, ok
}

// Manifests returns the manifest of every registered capability, so the
// composition root can report what it wired.
func (r *CapabilityRegistry) Manifests() []v1.Manifest {
	r.mu.RLock()
	defer r.mu.RUnlock()
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
	r.mu.RLock()
	defer r.mu.RUnlock()
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

// RequireRoles fails when no registered capability fills every role in required
// (ADR 0035, re-expressed by ADR 0063 over the composed set — core and extension
// together). Metadata and search are a required capability *class*: a Mosaic
// that cannot identify or find content is not a degraded Mosaic, it is inert,
// and the honest signal is refusing to serve rather than serving something that
// can do nothing.
//
// It is separate from Verify rather than folded into it because the two bind
// different things. Verify is about a module's internal consistency — a role
// declared but unbacked — and holds for any registry. This is about the
// *composition* being serviceable, so the serving composition root calls it and
// nothing else does; a test that builds a registry with one stream-only
// capability is not thereby broken.
func (r *CapabilityRegistry) RequireRoles(required ...v1.Role) error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, role := range required {
		filled := false
		for _, id := range r.sortedIDs() {
			if fills(r.byID[id], role) {
				filled = true
				break
			}
		}
		if !filled {
			return fmt.Errorf("no registered module provides the required %q role; a serving Mosaic needs one", role)
		}
	}
	return nil
}

// declares reports whether c names role in its manifest. Implementing the
// interface is not enough: a module that fills a role it did not declare is not
// resolvable by role at runtime, so counting it here would let boot succeed and
// every lookup fail.
func declares(c v1.Capability, role v1.Role) bool {
	for _, declared := range c.Manifest().Provides {
		if declared == role {
			return true
		}
	}
	return false
}

// fills reports whether c both declares role and backs it with the matching
// provider interface. **Every resolution below goes through this rather than
// through a bare type assertion**, and that is load-bearing rather than tidy.
//
// In process the two tests agree, because a compiled-in module that implements
// an interface also declares the role — so a bare assertion was correct by
// accident for as long as every capability was a local struct.
//
// It stops being correct the moment one is not. An out-of-process module is
// reached through a proxy that implements *every* provider interface
// unconditionally (ADR 0064: the registry must not be able to tell a proxy from
// a local struct, and Go type assertions cannot be made conditional at runtime).
// Against that proxy `c.(v1.StreamProvider)` always succeeds, so a
// metadata-only extension module would be enumerated by StreamProviders and
// asked for streams it cannot resolve — and, worse, RequireRoles would be
// satisfied by a module that fills nothing at all.
//
// The manifest is the honest answer, and the handshake is what makes it
// trustworthy: it refuses a module whose manifest declares a role it does not
// serve, so a declaration cannot be a lie by the time anything reads it here.
func fills(c v1.Capability, role v1.Role) bool {
	return declares(c, role) && roleImplemented(c, role)
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
	case v1.RoleArtwork:
		_, ok := c.(v1.ArtworkProvider)
		return ok
	case v1.RolePlayback:
		_, ok := c.(v1.PlaybackProvider)
		return ok
	case v1.RoleSettingsUI:
		_, ok := c.(v1.SettingsUIProvider)
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SearchProviderEntry
	for _, id := range r.sortedIDs() {
		if !fills(r.byID[id], v1.RoleSearch) {
			continue
		}
		if p, ok := r.byID[id].(v1.SearchProvider); ok {
			out = append(out, SearchProviderEntry{ModuleID: id, Provider: p})
		}
	}
	return out
}

// StreamProviderEntry pairs a stream-capable module's id with its provider, so a
// caller can read the module's settings before invoking it.
type StreamProviderEntry struct {
	ModuleID string
	Provider v1.StreamProvider
}

// StreamProviders returns every registered capability that fills RoleStream, in
// stable module-id order.
//
// It is the enumeration ADR 0073 needs: materialising asks *every* stream
// provider for playable locations, not only the module that sourced the
// metadata, because the two are different jobs and a metadata module fills no
// stream role at all.
func (r *CapabilityRegistry) StreamProviders() []StreamProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []StreamProviderEntry
	for _, id := range r.sortedIDs() {
		if !fills(r.byID[id], v1.RoleStream) {
			continue
		}
		if p, ok := r.byID[id].(v1.StreamProvider); ok {
			out = append(out, StreamProviderEntry{ModuleID: id, Provider: p})
		}
	}
	return out
}

// ArtworkProviderEntry pairs an artwork-capable module's id with its provider.
type ArtworkProviderEntry struct {
	ModuleID string
	Provider v1.ArtworkProvider
}

// ArtworkProviders returns every registered capability that fills RoleArtwork,
// in stable module-id order.
//
// Like StreamProviders it is a fan-out enumeration (ADR 0075): artwork is
// resolved for content the provider did not source, so the module that supplied
// the metadata has no special claim. Unlike stream enrichment, the caller keeps
// asking after the first provider answers — artwork candidates from several
// sources union into one set rather than competing, so there is no first-wins
// rule and no cross-provider dedup problem to leave open.
func (r *CapabilityRegistry) ArtworkProviders() []ArtworkProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []ArtworkProviderEntry
	for _, id := range r.sortedIDs() {
		if !fills(r.byID[id], v1.RoleArtwork) {
			continue
		}
		if p, ok := r.byID[id].(v1.ArtworkProvider); ok {
			out = append(out, ArtworkProviderEntry{ModuleID: id, Provider: p})
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
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []CatalogProviderEntry
	for _, id := range r.sortedIDs() {
		if !fills(r.byID[id], v1.RoleCatalog) {
			continue
		}
		if p, ok := r.byID[id].(v1.CatalogProvider); ok {
			out = append(out, CatalogProviderEntry{ModuleID: id, Provider: p})
		}
	}
	return out
}

// CatalogProvider returns the catalog provider registered under id, if that
// capability fills RoleCatalog.
func (r *CapabilityRegistry) CatalogProvider(id string) (v1.CatalogProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	if !fills(c, v1.RoleCatalog) {
		return nil, false
	}
	p, ok := c.(v1.CatalogProvider)
	return p, ok
}

// MetadataProvider returns the metadata provider registered under id, if that
// capability fills RoleMetadata.
func (r *CapabilityRegistry) MetadataProvider(id string) (v1.MetadataProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	if !fills(c, v1.RoleMetadata) {
		return nil, false
	}
	p, ok := c.(v1.MetadataProvider)
	return p, ok
}

// SubtitlesProvider returns the subtitles provider registered under id, if that
// capability fills RoleSubtitles (ADR 0037). The consumer is a future player; the
// resolver exists so that consumer has a seam to reach it, like the others.
func (r *CapabilityRegistry) SubtitlesProvider(id string) (v1.SubtitlesProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	if !fills(c, v1.RoleSubtitles) {
		return nil, false
	}
	p, ok := c.(v1.SubtitlesProvider)
	return p, ok
}

// SettingsUIProvider returns the settings-UI provider registered under id, if
// that capability fills RoleSettingsUI (ADR 0038) — how the module-settings host
// resolves a module's contributed settings screen.
func (r *CapabilityRegistry) SettingsUIProvider(id string) (v1.SettingsUIProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	if !fills(c, v1.RoleSettingsUI) {
		return nil, false
	}
	p, ok := c.(v1.SettingsUIProvider)
	return p, ok
}

// PlaybackProviderEntry pairs a playback-capable module's id with its provider,
// so a caller can read the module's settings before invoking it.
type PlaybackProviderEntry struct {
	ModuleID string
	Provider v1.PlaybackProvider
}

// PlaybackProviders returns every registered capability that fills RolePlayback
// (ADR 0045), in stable module-id order. It is the first *consumer* enumeration
// here — every other one above resolves a source.
//
// It returns a list rather than the single provider today's install has because
// the question it answers has two callers with different needs: playback
// resolution wants one provider, and ADR 0036's affordance gate wants to know
// whether *any* consumer is installed.
func (r *CapabilityRegistry) PlaybackProviders() []PlaybackProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []PlaybackProviderEntry
	for _, id := range r.sortedIDs() {
		if !fills(r.byID[id], v1.RolePlayback) {
			continue
		}
		if p, ok := r.byID[id].(v1.PlaybackProvider); ok {
			out = append(out, PlaybackProviderEntry{ModuleID: id, Provider: p})
		}
	}
	return out
}

// PlaybackProvider returns the playback provider registered under id, if that
// capability fills RolePlayback.
func (r *CapabilityRegistry) PlaybackProvider(id string) (v1.PlaybackProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	if !fills(c, v1.RolePlayback) {
		return nil, false
	}
	p, ok := c.(v1.PlaybackProvider)
	return p, ok
}

// SettingsUIProviderEntry names a module that contributes a settings screen. It
// carries the manifest's Name as well as its id because this is the one
// enumeration whose result is *rendered*: an index listing "aiostreams" and
// "stremio" makes a reader work out which is which, and the module already
// declares a human label.
type SettingsUIProviderEntry struct {
	ModuleID string
	Name     string
}

// SettingsUIProviders returns every registered capability that fills
// RoleSettingsUI (ADR 0038), in stable module-id order.
//
// It is what turns a module's settings screen from something the Platform can
// render into something a user can reach. The settings host used to name one
// module by constant, so every module that contributed a screen after the first
// had one nobody could open — `module-tmdb` shipped a whole credential form in
// that state, which is the "capability with no client path" case: it worked, and
// it was owed.
//
// It reads Manifest() per call rather than caching a label at registration. The
// registry is populated once at composition and read at invocation, so the cost
// is a map walk and a few struct copies on a settings render, and the
// alternative is a second source of truth for a module's name.
func (r *CapabilityRegistry) SettingsUIProviders() []SettingsUIProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SettingsUIProviderEntry
	for _, id := range r.sortedIDs() {
		c := r.byID[id]
		if _, ok := c.(v1.SettingsUIProvider); !ok {
			continue
		}
		// Declared as well as implemented: a module that fills a role it did not
		// declare is not resolvable by role, so listing it would offer a screen
		// the host then refuses to open.
		if !declares(c, v1.RoleSettingsUI) {
			continue
		}
		name := c.Manifest().Name
		if name == "" {
			name = id
		}
		out = append(out, SettingsUIProviderEntry{ModuleID: id, Name: name})
	}
	return out
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
