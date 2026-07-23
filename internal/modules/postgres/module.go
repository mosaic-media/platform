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
			"TelemetryQueryStore",
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
	// TelemetryQueries reads stored telemetry back (ADR 0058).
	TelemetryQueries contracts.TelemetryQueryStore
	Clock            contracts.Clock
	IDs              contracts.IDGenerator
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
		Pool:             pool,
		UnitOfWork:       storage.UnitOfWork(),
		Users:            NewUserStore(pool),
		Sessions:         NewSessionStore(pool),
		Permissions:      NewPermissionStore(pool),
		Config:           NewConfigStore(pool),
		Outbox:           NewEventOutbox(pool),
		Credentials:      NewCredentialStore(pool),
		Nodes:            NewNodeStore(pool),
		Parts:            NewPartStore(pool),
		Relations:        NewRelationStore(pool),
		SourceBindings:   NewSourceBindingStore(pool),
		ModuleSettings:   NewModuleSettingsStore(pool),
		UserPreferences:  NewUserPreferenceStore(pool),
		TelemetryQueries: NewTelemetryQueryStore(pool),
		Clock:            NewClock(),
		IDs:              NewIDGenerator(),
		ContentIDs:       NewUUIDv7Generator(),
		Health:           NewHealthProbe(pool),
		HealthReporter:   NewComponentHealthReporter(pool),
	}
}

// compile-time assertion that the module satisfies the built-in Module
// interface.
var _ builtin.Module = Module{}
