// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package extensions owns the runtime lifecycle of extension modules (platform#49,
// platform#51): installing and uninstalling them at a user's request, re-adopting
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
// the Platform serves — that is the whole point of platform#51's runtime model —
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
	metrics   *telemetry.MetricCollector
	findings  Findings

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
	// Store is the durable record of what is installed (platform#51).
	Store contracts.InstalledExtensionStore
	// Content is what an adopted module calls back into — the application service,
	// so each module write re-authorises as the invoking user (platform#13).
	Content v1.ContentService
	// Clock stamps an install record's time.
	Clock contracts.Clock
	// Policy is the supervisor's crash-loop policy.
	Policy extension.RestartPolicy
	// Root is the telemetry root; per-module telemetry (sdk#5) and the
	// manager's own diagnostics are derived from it.
	Root *telemetry.Logger
	// Metrics is where an adopted module's counters and histograms land
	// (sdk#9). Optional: nil discards them, which is what a Manager built
	// without one did before the surface existed.
	Metrics *telemetry.MetricCollector
	// Findings is the resolution register (platform#74). Optional: a Manager
	// built without one logs and skips, which is what it did before the
	// register existed.
	Findings Findings
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
		metrics:   d.Metrics,
		findings:  d.Findings,
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

// SetFindings supplies the resolution register, wired after construction for
// the same reason SetContent is: the Service holds the register and the Manager
// is built before the Service exists.
func (m *Manager) SetFindings(f Findings) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.findings = f
}

// InstalledExtensions is the durable installed set, read from the store — the
// runtime-independent view a settings surface reads, distinct from the live
// process list Installed() reports.
func (m *Manager) InstalledExtensions(ctx context.Context) ([]domain.InstalledExtension, error) {
	return m.store.List(ctx)
}

// Available lists what the official repository offers, for a browse-and-install
// surface (platform#51). It reaches the repository over the network — the caller is
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
			Repository:  extension.OfficialRepositoryName,
			ModuleID:    man.ID,
			Name:        man.Name,
			Version:     man.Version,
			Provides:    provides,
			Description: man.Description,
		})
	}
	return out, nil
}

// Findings is the slice of the resolution register this package needs
// (platform#74): somewhere to state that a capability is missing, and somewhere to
// withdraw that statement when it comes back.
//
// An interface rather than the Service, so this package keeps depending on the
// application layer through the narrowest surface that does its job.
type Findings interface {
	RaiseIssue(ctx context.Context, issue domain.Issue) error
	ClearIssueSituation(ctx context.Context, t domain.IssueType, c domain.IssueContext, reference string) error
}

// AdoptInstalled brings up every installed extension at boot (platform#51). Each is
// re-verified against its cached manifest and spawned; a failure is a degraded
// capability — never fatal, because extensions fill no required role class. It
// is called once, before the serve loop.
//
// A failure is also recorded on the resolution register (platform#74). Logging
// and skipping is not enough: the capability is simply absent, nothing fails,
// nothing is said, and the line scrolls away before anybody wonders why their
// addons stopped working. A success withdraws any finding from a previous
// boot, so the register says what is wrong now rather than what has ever been
// wrong.
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
			m.raiseUnavailable(ctx, log, rec.ModuleID, adoptErr)
			continue
		}
		if _, err := m.spawnLocked(adopted); err != nil {
			log.Error("installed extension could not be launched; capability degraded",
				telemetry.String("module", rec.ModuleID), telemetry.Err(err))
			m.raiseUnavailable(ctx, log, rec.ModuleID, err)
			continue
		}
		log.Info("adopted extension module",
			telemetry.String("module", rec.ModuleID),
			telemetry.String("version", rec.Version),
			telemetry.String("repository", rec.Repository))
		m.clearUnavailable(ctx, log, rec.ModuleID)
	}
	return nil
}

// Install fetches, verifies and spawns a module from a trusted repository, then
// records it in the durable set (platform#51). Verify-and-spawn happen first, so a
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
		// One key vouches for everything a repository distributes (platform#40), so
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
// (platform#51). It is idempotent — uninstalling one that is not installed stops
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
	tel := app.NewModuleTelemetry(m.root.For("module."+inst.ModuleID), inst.ModuleID, m.metrics)
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

// raiseUnavailable states that a module is not running.
//
// The register's own failure is logged and swallowed. This runs on the boot
// path, and a Platform that refused to start because it could not write down
// that an optional module did not start would turn a degradation into an
// outage.
func (m *Manager) raiseUnavailable(ctx context.Context, log *telemetry.Logger, moduleID string, cause error) {
	if m.findings == nil {
		return
	}
	err := m.findings.RaiseIssue(ctx, domain.Issue{
		Type:      domain.IssueExtensionUnavailable,
		Context:   domain.ContextExtension,
		Reference: moduleID,
		Source:    domain.SourcePlatform,
		Detail:    cause.Error(),
	})
	if err != nil {
		log.Error("could not record that an extension is unavailable",
			telemetry.String("module", moduleID), telemetry.Err(err))
	}
}

// clearUnavailable withdraws that statement when the module comes up.
func (m *Manager) clearUnavailable(ctx context.Context, log *telemetry.Logger, moduleID string) {
	if m.findings == nil {
		return
	}
	err := m.findings.ClearIssueSituation(ctx,
		domain.IssueExtensionUnavailable, domain.ContextExtension, moduleID)
	if err != nil {
		log.Error("could not withdraw an extension finding",
			telemetry.String("module", moduleID), telemetry.Err(err))
	}
}
