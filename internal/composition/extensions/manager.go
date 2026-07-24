// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package extensions owns the runtime lifecycle of extension modules (ADR 0079,
// ADR 0081): installing and uninstalling them at a user's request, re-adopting
// the installed set at boot, and holding the live supervised processes so an
// uninstall stops the right one.
//
// It lives under composition/ because it is the one component that ties three
// things together that do not know about each other — the installer (which
// downloads and verifies), the durable store (which records what a user
// installed), and the capability registry (which the rest of the Platform
// resolves through). Wiring independent parts is the composition root's job, and
// importing both an adapter and the app registry is a thing only the composition
// root may do.
package extensions

import (
	"context"
	"fmt"
	"sync"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/adapters/extension"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// Manager brings the installed set to life and keeps it in step with a user's
// install and uninstall actions. Install and uninstall are safe to call while
// the Platform serves — that is the whole point of ADR 0081's runtime model —
// so the live-handle map and the compound register-and-track operations are
// guarded by a mutex, while the capability registry it writes to guards itself.
type Manager struct {
	installer *extension.Installer
	registry  *app.CapabilityRegistry
	store     contracts.InstalledExtensionStore
	content   v1.ContentService
	clock     contracts.Clock
	policy    extension.RestartPolicy
	root      *telemetry.Logger

	mu   sync.Mutex
	live map[string]*extension.Supervised
}

// Deps are what a Manager needs; every field is required.
type Deps struct {
	// Installer downloads, verifies and re-adopts modules from a trusted
	// repository.
	Installer *extension.Installer
	// Registry is where an adopted module's capability is registered so the rest
	// of the Platform resolves it, and unregistered on uninstall.
	Registry *app.CapabilityRegistry
	// Store is the durable record of what is installed (ADR 0081).
	Store contracts.InstalledExtensionStore
	// Content is what an adopted module calls back into — the application service,
	// so each module write re-authorises as the invoking user (ADR 0017).
	Content v1.ContentService
	// Clock stamps an install record's time.
	Clock contracts.Clock
	// Policy is the supervisor's crash-loop policy.
	Policy extension.RestartPolicy
	// Root is the telemetry root; per-module telemetry (ADR 0059) and the
	// manager's own diagnostics are derived from it.
	Root *telemetry.Logger
}

// NewManager wires a Manager from its dependencies.
func NewManager(d Deps) *Manager {
	return &Manager{
		installer: d.Installer,
		registry:  d.Registry,
		store:     d.Store,
		content:   d.Content,
		clock:     d.Clock,
		policy:    d.Policy,
		root:      d.Root,
		live:      make(map[string]*extension.Supervised),
	}
}

// SetContent supplies the ContentService adopted modules call back into. It
// exists to break a construction cycle: the Service needs the Manager (to drive
// install and uninstall), and the Manager needs the Service (as the callback
// target), so one side is wired after both exist. The composition root calls
// this once, after building the Service and before adopting or installing
// anything — every spawn reads it, and no spawn happens before then.
func (m *Manager) SetContent(content v1.ContentService) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.content = content
}

// InstalledExtensions is the durable installed set, read from the store — the
// runtime-independent view a settings surface reads, distinct from the live
// process list Installed() reports.
func (m *Manager) InstalledExtensions(ctx context.Context) ([]domain.InstalledExtension, error) {
	return m.store.List(ctx)
}

// Available lists what the official repository offers, for a browse-and-install
// surface (ADR 0081). It reaches the repository over the network — the caller is
// a user opening the extensions surface — and returns the catalogue projected to
// what that surface shows.
func (m *Manager) Available(ctx context.Context) ([]app.ExtensionCatalogueEntry, error) {
	manifests, err := m.installer.Catalogue(ctx, extension.OfficialRepositoryName)
	if err != nil {
		return nil, err
	}
	out := make([]app.ExtensionCatalogueEntry, 0, len(manifests))
	for _, man := range manifests {
		provides := make([]string, len(man.Provides))
		for i, r := range man.Provides {
			provides[i] = string(r)
		}
		out = append(out, app.ExtensionCatalogueEntry{
			Repository: extension.OfficialRepositoryName,
			ModuleID:   man.ID,
			Name:       man.Name,
			Version:    man.Version,
			Provides:   provides,
		})
	}
	return out, nil
}

// AdoptInstalled brings up every installed extension at boot (ADR 0081). Each is
// re-verified against its cached manifest and spawned; a failure is a degraded
// capability — logged and skipped, never fatal, because extensions fill no
// required role class. It is called once, before the serve loop.
func (m *Manager) AdoptInstalled(ctx context.Context) error {
	records, err := m.store.List(ctx)
	if err != nil {
		return fmt.Errorf("reading installed extensions: %w", err)
	}

	log := m.root.For("extensions")
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, rec := range records {
		adopted, adoptErr := m.installer.Adopt(ctx, rec.Repository, rec.ModuleID)
		if adoptErr != nil {
			log.Error("installed extension could not be adopted; capability degraded",
				telemetry.String("module", rec.ModuleID),
				telemetry.String("repository", rec.Repository),
				telemetry.Err(adoptErr))
			continue
		}
		if _, err := m.spawnLocked(adopted); err != nil {
			log.Error("installed extension could not be launched; capability degraded",
				telemetry.String("module", rec.ModuleID), telemetry.Err(err))
			continue
		}
		log.Info("adopted extension module",
			telemetry.String("module", rec.ModuleID),
			telemetry.String("version", rec.Version),
			telemetry.String("repository", rec.Repository))
	}
	return nil
}

// Install fetches, verifies and spawns a module from a trusted repository, then
// records it in the durable set (ADR 0081). Verify-and-spawn happen first, so a
// module that cannot be brought up fails the install without leaving a record;
// then the record is written, so a durable install only ever names a module that
// ran. A persist failure tears the just-spawned process back down, so a failed
// install leaves nothing behind.
//
// The network fetch runs outside the lock — it can take seconds, and an
// uninstall must not block behind it.
func (m *Manager) Install(ctx context.Context, repository, moduleID string) (domain.InstalledExtension, error) {
	installed, err := m.installer.Install(ctx, repository, moduleID)
	if err != nil {
		return domain.InstalledExtension{}, err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if _, err := m.spawnLocked(installed); err != nil {
		return domain.InstalledExtension{}, err
	}

	rec := domain.InstalledExtension{
		ModuleID:   installed.ModuleID,
		Repository: installed.Repository,
		Version:    installed.Version,
		// One key vouches for everything a repository distributes (ADR 0065), so
		// the repository is the signer identity in this model.
		SignedBy:    installed.Repository,
		InstalledAt: m.clock.Now(),
	}
	if _, err := m.store.Upsert(ctx, rec); err != nil {
		// Roll the running module back so a failed persist leaves no process the
		// durable set does not know about.
		m.stopLocked(installed.ModuleID)
		return domain.InstalledExtension{}, err
	}
	return rec, nil
}

// Uninstall stops a module, makes it unresolvable, and drops its record
// (ADR 0081). It is idempotent — uninstalling one that is not installed stops
// nothing and removes nothing and is not an error. The order is the reverse of
// install: unregister first so nothing new routes to it, stop the process, then
// remove the record.
func (m *Manager) Uninstall(ctx context.Context, moduleID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.stopLocked(moduleID)
	if err := m.store.Remove(ctx, moduleID); err != nil {
		return err
	}
	return nil
}

// Installed reports the modules currently live, in the sense that matters to an
// admin surface: a running supervised process the registry resolves. It is the
// runtime view; the durable set is the store's List.
func (m *Manager) Installed() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	ids := make([]string, 0, len(m.live))
	for id := range m.live {
		ids = append(ids, id)
	}
	return ids
}

// Close stops every live module process, so a module never outlives the Platform
// that spawned it. Called when the Platform stops.
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for id := range m.live {
		m.stopLocked(id)
	}
}

// spawnLocked supervises an installed module, registers its capability, and
// tracks the handle, replacing any earlier one for the same id — a reinstall
// swaps the process. The caller holds m.mu.
func (m *Manager) spawnLocked(inst extension.Installed) (*extension.Supervised, error) {
	tel := app.NewModuleTelemetry(m.root.For("module."+inst.ModuleID), inst.ModuleID)
	inst.Config.Content = m.content
	inst.Config.Telemetry = tel
	sup, err := extension.Supervise(inst.Config, m.policy, tel)
	if err != nil {
		return nil, err
	}
	if old, ok := m.live[inst.ModuleID]; ok {
		old.Close()
	}
	m.registry.Register(sup)
	m.live[inst.ModuleID] = sup
	return sup, nil
}

// stopLocked unregisters, stops and forgets a module if it is live. The caller
// holds m.mu. Unregister runs whether or not a live handle exists, so a stale
// registration can never survive an uninstall.
func (m *Manager) stopLocked(moduleID string) {
	m.registry.Unregister(moduleID)
	if sup, ok := m.live[moduleID]; ok {
		sup.Close()
		delete(m.live, moduleID)
	}
}
