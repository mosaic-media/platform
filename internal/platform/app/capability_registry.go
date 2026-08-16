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
// registered and unregistered while the Platform serves — a user installs or
// uninstalls one at runtime (platform#51) — so the map is guarded by a RWMutex:
// a resolution during serving takes the read lock, an install or uninstall the
// write lock.
//
// It lives in the app package rather than under composition/ so the Service can
// hold it without an import cycle: it depends only on the published SDK,
// exactly as a module does.
type CapabilityRegistry struct {
	mu sync.RWMutex
	// fallback holds the ids registered as a floor rather than as a first
	// choice. A read-role fan-out consults these only when the ordinary
	// providers between them returned nothing.
	byID     map[string]v1.Capability
	fallback map[string]bool
}

// NewCapabilityRegistry returns an empty registry.
func NewCapabilityRegistry() *CapabilityRegistry {
	return &CapabilityRegistry{byID: make(map[string]v1.Capability), fallback: make(map[string]bool)}
}

// Register adds a capability under its manifest id, replacing any earlier one
// under the same id — a reinstall swaps the running proxy for a fresh one. Safe
// to call while the Platform serves: a runtime install registers here.
func (r *CapabilityRegistry) Register(c v1.Capability) {
	r.register(c, false)
}

// RegisterFallback adds a capability that fills its read roles only when no
// ordinary provider could — the guarantee-clause floor rather than a peer.
//
// Ranking is needed because "one or more providers per role class"
// (platform#38) unioned across the browse roles draws the same titles twice from
// two sources. Ranking does not contradict module-cinemeta#1's arity: both
// providers are still registered, both still fill the class, and a deployment
// with only the floor gets exactly what it got before.
//
// A fallback is not a module the Platform trusts less; it is a statement about
// which source should be visible when both can answer. It stays a composition
// decision (platform#4) rather than a property a module asserts about itself.
func (r *CapabilityRegistry) RegisterFallback(c v1.Capability) {
	r.register(c, true)
}

func (r *CapabilityRegistry) register(c v1.Capability, fallback bool) {
	id := c.Manifest().ID
	r.mu.Lock()
	defer r.mu.Unlock()
	r.byID[id] = c
	// Re-registration re-states the tier: a reinstall must not silently inherit
	// the previous registration's standing.
	if fallback {
		r.fallback[id] = true
	} else {
		delete(r.fallback, id)
	}
}

// Unregister removes the capability under id, if any. It is how a runtime
// uninstall (platform#51) makes a module unresolvable: after it returns, no
// lookup or role enumeration finds the module, so nothing routes to a process
// that is being torn down. Removing an id that is not present is a no-op.
func (r *CapabilityRegistry) Unregister(id string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.byID, id)
	delete(r.fallback, id)
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
// in Manifest.Provides (sdk#2). A role named but not backed by the matching
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
// (platform#23, re-expressed by platform#38 over the composed set — core and
// extension together). Metadata and search are a required capability class: a
// Mosaic that cannot identify or find content is inert, so refusing to serve is
// the honest signal.
//
// It is separate from Verify because the two bind different things. Verify is
// about a module's internal consistency — a role declared but unbacked — and
// holds for any registry. This is about the composition being serviceable, so
// only the serving composition root calls it; a test that builds a registry
// with one stream-only capability is not thereby broken.
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
// provider interface. Every resolution below must go through this rather than
// through a bare type assertion.
//
// An out-of-process module is reached through a proxy that implements every
// provider interface unconditionally (platform#39: the registry must not be able
// to tell a proxy from a local struct, and Go type assertions cannot be made
// conditional at runtime). Against that proxy c.(v1.StreamProvider) always
// succeeds, so a metadata-only extension module would be enumerated by
// StreamProviders and asked for streams it cannot resolve — and RequireRoles
// would be satisfied by a module that fills nothing at all.
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
	// Fallback marks a provider consulted only when the ordinary ones returned
	// nothing. See CapabilityRegistry.RegisterFallback.
	Fallback bool
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
			out = append(out, SearchProviderEntry{ModuleID: id, Provider: p, Fallback: r.fallback[id]})
		}
	}
	return out
}

// SearchProvider returns the search provider registered under id, if that
// capability fills RoleSearch.
//
// The singular form exists for a saved provider search (platform#60): a query
// rule is a durable statement addressed to one source, not a fan-out. Fanning a
// saved rule across every installed search provider would change what the rule
// means whenever somebody installed an extension.
func (r *CapabilityRegistry) SearchProvider(id string) (v1.SearchProvider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	c, ok := r.byID[id]
	if !ok {
		return nil, false
	}
	if !fills(c, v1.RoleSearch) {
		return nil, false
	}
	p, ok := c.(v1.SearchProvider)
	return p, ok
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
// It is the enumeration platform#46 needs: materialising asks every stream
// provider for playable locations, not only the module that sourced the
// metadata, because a metadata module fills no stream role at all.
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

// SubtitlesProviderEntry pairs a subtitles-capable module's id with its
// provider, so a caller can read the module's settings before invoking it.
type SubtitlesProviderEntry struct {
	ModuleID string
	Provider v1.SubtitlesProvider
}

// SubtitlesProviders returns every registered capability that fills
// RoleSubtitles, in stable module-id order.
//
// Subtitles fan out for the same reason streams do (platform#46, platform#83): a
// provider is asked about content it did not source, so every installed one is
// asked rather than only the module that supplied the metadata. Resolving one by
// id is not enough, because nothing knows an id to ask for.
func (r *CapabilityRegistry) SubtitlesProviders() []SubtitlesProviderEntry {
	r.mu.RLock()
	defer r.mu.RUnlock()
	var out []SubtitlesProviderEntry
	for _, id := range r.sortedIDs() {
		if !fills(r.byID[id], v1.RoleSubtitles) {
			continue
		}
		if p, ok := r.byID[id].(v1.SubtitlesProvider); ok {
			out = append(out, SubtitlesProviderEntry{ModuleID: id, Provider: p})
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
// Like StreamProviders it is a fan-out enumeration (sdk#6): artwork is resolved
// for content the provider did not source, so the module that supplied the
// metadata has no special claim. Unlike stream enrichment, the caller keeps
// asking after the first provider answers — artwork candidates from several
// sources union into one set rather than competing, so there is no first-wins
// rule.
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
	// Fallback marks a provider consulted only when the ordinary ones returned
	// nothing. See CapabilityRegistry.RegisterFallback.
	Fallback bool
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
			out = append(out, CatalogProviderEntry{ModuleID: id, Provider: p, Fallback: r.fallback[id]})
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
// capability fills RoleSubtitles (module-stremio-addons#1).
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
// that capability fills RoleSettingsUI (sdk#4) — how the module-settings host
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
// (platform#25), in stable module-id order. It enumerates consumers, where every
// other enumeration here resolves a source.
//
// It returns a list rather than the single provider today's install has because
// it serves two callers: playback resolution wants one provider, and
// platform#24's affordance gate wants to know whether any consumer is installed.
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
// enumeration whose result is rendered, and a module id is not a label a reader
// can use.
type SettingsUIProviderEntry struct {
	ModuleID string
	Name     string
}

// SettingsUIProviders returns every registered capability that fills
// RoleSettingsUI (sdk#4), in stable module-id order.
//
// It is what turns a module's settings screen from something the Platform can
// render into something a user can reach. The settings host must enumerate
// rather than name a module by constant, or a module that contributes a screen
// has one nobody can open.
//
// It reads Manifest() per call rather than caching a label at registration: the
// cost is a map walk and a few struct copies on a settings render, and caching
// would be a second source of truth for a module's name.
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
// enumeration uses. It takes no lock of its own: callers must already hold
// r.mu, at least for reading.
func (r *CapabilityRegistry) sortedIDs() []string {
	ids := make([]string, 0, len(r.byID))
	for id := range r.byID {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}
