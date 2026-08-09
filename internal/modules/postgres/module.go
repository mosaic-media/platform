// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/composition/builtin"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Module identity, declared in the manifest and usable for later SDK
// compatibility checks.
const (
	ModuleID      = "mosaic.platform.module.postgres"
	ModuleVersion = "v1"
)

// Module is the built-in PostgreSQL module. It is the mandatory first
// storage adapter, compiled in and required, but presented through the same
// manifest shape an external Module would use.
type Module struct{}

// New returns the built-in PostgreSQL module.
func New() Module {
	return Module{}
}

// Manifest declares the Platform contracts this module fulfills. UnitOfWork
// through HealthProbe are the module's original contract set; CredentialStore
// is included because the Identity slice added it to the contract set (and to
// contracts.Tx) after that set was written, and this module genuinely fulfills
// it.
func (Module) Manifest() builtin.Manifest {
	return builtin.Manifest{
		ID:      ModuleID,
		Version: ModuleVersion,
		Fulfills: []string{
			"UnitOfWork",
			"UserStore",
			"SessionStore",
			"PermissionStore",
			"ConfigStore",
			"EventOutbox",
			"CredentialStore",
			"NodeStore",
			"PartStore",
			"RelationStore",
			"SourceBindingStore",
			// ModuleSettingsStore was fulfilled from ADR 0021 and never
			// declared here; UserPreferenceStore joins it. The manifest is
			// what a Supervisor would check a build against, so a store
			// missing from it is a quiet lie about what this module provides.
			"ModuleSettingsStore",
			"UserPreferenceStore",
			"PlaybackResolutionStore",
			"PlaybackStateStore",
			"TelemetryQueryStore",
			"TelemetryMaintenanceStore",
			"JobStore",
			// What the library should contain (ADR 0104) and what a provider
			// said about it (ADR 0107). Declared for the reason the two above
			// were added late: the manifest is what a Supervisor would check a
			// build against, so a store missing from it is a quiet lie about
			// what this module provides.
			"LibraryRuleStore",
			"NodeMetadataStore",
			"SourceSnapshotStore",
			"WatchAvailabilityStore",
			"TokenStore",
			"Clock",
			"IDGenerator",
			"HealthProbe",
			"ComponentHealthReporter",
		},
	}
}

// ContractSet is the bundle of Platform contract implementations the module
// provides once connected. The direct (pool-backed) stores serve the
// authentication and query read paths; UnitOfWork serves the transactional
// write path. Callers own Pool and must Close it on shutdown.
type ContractSet struct {
	Pool *pgxpool.Pool

	UnitOfWork     contracts.UnitOfWork
	Users          contracts.UserStore
	Sessions       contracts.SessionStore
	Permissions    contracts.PermissionStore
	Config         contracts.ConfigStore
	Outbox         contracts.EventOutbox
	Credentials    contracts.CredentialStore
	Nodes          contracts.NodeStore
	Parts          contracts.PartStore
	Relations      contracts.RelationStore
	SourceBindings contracts.SourceBindingStore
	ModuleSettings contracts.ModuleSettingsStore
	// UserPreferences is the direct read handle for a user's own settings.
	UserPreferences contracts.UserPreferenceStore
	// InstalledExtensions is the direct read handle for the durable set of
	// installed extension modules (ADR 0081), read at boot to re-adopt them.
	InstalledExtensions contracts.InstalledExtensionStore
	// PlaybackResolutions caches where a release's bytes are, per capability
	// class (ADR 0049). Direct rather than transaction-scoped: it is written
	// after playback has already started, so it must not be inside anything's
	// unit of work.
	PlaybackResolutions contracts.PlaybackResolutionStore
	// PlaybackStates is the direct read handle for where a viewer got to
	// (ADR 0046) — a detail screen's resume offset and the continue-watching
	// rail. Writes go through the UnitOfWork.
	PlaybackStates contracts.PlaybackStateStore
	// TelemetryQueries reads stored telemetry back (ADR 0058).
	TelemetryQueries contracts.TelemetryQueryStore
	// TelemetryMaintenance creates and drops the partitions telemetry lives
	// in. It is the same *TelemetryStore the composition root writes records
	// through, exposed here as the contract the retention job drives so that
	// nothing above the module has to name a PostgreSQL type.
	TelemetryMaintenance contracts.TelemetryMaintenanceStore
	// Jobs is the background-work queue (ADR 0017). Pool-backed rather than
	// transaction-scoped: a claim is its own transaction.
	Jobs contracts.JobStore
	// LibraryRules is the direct read handle for what the library should
	// contain (ADR 0104). Writes go through the UnitOfWork, where a rule and
	// the event announcing it commit together.
	LibraryRules contracts.LibraryRuleStore
	// Issues is the resolution register (ADR 0119).
	Issues contracts.IssueStore
	// NodeMetadata is the direct read handle for a stored provider answer
	// (ADR 0107) — what a library detail renders from.
	NodeMetadata contracts.NodeMetadataStore
	// Snapshots is the last good answer per source and question (ADR 0052) —
	// what a source-backed browse screen renders from while its sources are
	// cold, slow or down. Pool-backed rather than transaction-scoped, like the
	// playback resolution cache: it is written on a read path and commits with
	// nothing.
	Snapshots contracts.SourceSnapshotStore
	// WatchAvailability is the queryable projection of where a work can be
	// watched (roadmap M2.5) — what the streaming-service facet filters on and
	// what the refresh keeps current.
	WatchAvailability contracts.WatchAvailabilityStore
	// Tokens is the direct read handle for a session's bearer pair (ADR 0102):
	// an access token is validated on every call, so it must not open one.
	Tokens contracts.TokenStore
	Clock  contracts.Clock
	IDs    contracts.IDGenerator
	// ContentIDs generates UUIDv7 identifiers for the content model, whose
	// tables use native uuid columns. IDs stays UUIDv4 for the
	// infrastructure tables, which keep their text ids and are not migrated
	// (ADR 0013).
	ContentIDs     contracts.IDGenerator
	Health         contracts.HealthProbe
	HealthReporter contracts.ComponentHealthReporter
}

// Open connects to PostgreSQL, runs migrations (failing fast on a missing,
// incompatible or partially applied schema), and returns the wired contract
// set. On any failure it closes the pool it opened and
// returns a Platform error, so a caller never receives a half-initialised
// module.
func (Module) Open(ctx context.Context, dsn string) (*ContractSet, error) {
	pool, err := Connect(ctx, dsn)
	if err != nil {
		return nil, err
	}

	if err := Migrate(ctx, pool); err != nil {
		pool.Close()
		return nil, err
	}

	return newContractSet(pool), nil
}

// Bind wires a contract set over an already-open, already-migrated pool. It
// performs no I/O. Tests and callers that manage migrations separately use
// this; Open is the normal path.
func (Module) Bind(pool *pgxpool.Pool) *ContractSet {
	return newContractSet(pool)
}

func newContractSet(pool *pgxpool.Pool) *ContractSet {
	// The UnitOfWork is reached through the StorageAdapter port rather than
	// constructed directly, so storage is a swappable port even though
	// ContractSet's shape and main.go are unchanged.
	storage := NewStorageAdapter(pool)
	return &ContractSet{
		Pool:            pool,
		UnitOfWork:      storage.UnitOfWork(),
		Users:           NewUserStore(pool),
		Sessions:        NewSessionStore(pool),
		Permissions:     NewPermissionStore(pool),
		Config:          NewConfigStore(pool),
		Outbox:          NewEventOutbox(pool),
		Credentials:     NewCredentialStore(pool),
		Nodes:           NewNodeStore(pool),
		Parts:           NewPartStore(pool),
		Relations:       NewRelationStore(pool),
		SourceBindings:  NewSourceBindingStore(pool),
		ModuleSettings:  NewModuleSettingsStore(pool),
		UserPreferences: NewUserPreferenceStore(pool),

		InstalledExtensions: NewInstalledExtensionStore(pool),
		PlaybackResolutions: NewPlaybackResolutionStore(pool),
		PlaybackStates:      NewPlaybackStateStore(pool),

		TelemetryQueries:     NewTelemetryQueryStore(pool),
		TelemetryMaintenance: NewTelemetryStore(pool),
		Jobs:                 NewJobStore(pool),
		LibraryRules:         NewLibraryRuleStore(pool),
		NodeMetadata:         NewNodeMetadataStore(pool),
		Issues:               NewIssueStore(pool),
		Snapshots:            NewSourceSnapshotStore(pool),
		WatchAvailability:    NewWatchAvailabilityStore(pool),
		Tokens:               NewTokenStore(pool),

		Clock:          NewClock(),
		IDs:            NewIDGenerator(),
		ContentIDs:     NewUUIDv7Generator(),
		Health:         NewHealthProbe(pool),
		HealthReporter: NewComponentHealthReporter(pool),
	}
}

// compile-time assertion that the module satisfies the built-in Module
// interface.
var _ builtin.Module = Module{}
