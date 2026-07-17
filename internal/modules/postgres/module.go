package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/composition/builtin"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// Module identity, declared in the manifest and usable for later SDK
// compatibility checks.
const (
	ModuleID      = "mosaic.platform.module.postgres"
	ModuleVersion = "v1"
)

// Module is the built-in PostgreSQL module. It is the mandatory first
// storage adapter (MEG-015 §05), compiled in and required, but presented
// through the same manifest shape an external Module would use.
type Module struct{}

// New returns the built-in PostgreSQL module.
func New() Module {
	return Module{}
}

// Manifest declares the Platform contracts this module fulfills. UnitOfWork
// through HealthProbe are the set named in MEG-015 §12's PostgreSQL row;
// CredentialStore is included because the Identity slice added it to the
// contract set (and to contracts.Tx) after MEG-015 §03's original table was
// written, and this module genuinely fulfills it.
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
	Clock          contracts.Clock
	IDs            contracts.IDGenerator
	Health         contracts.HealthProbe
	HealthReporter contracts.ComponentHealthReporter
}

// Open connects to PostgreSQL, runs migrations (failing fast on a missing,
// incompatible or partially applied schema — MEG-015 §05), and returns the
// wired contract set. On any failure it closes the pool it opened and
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
	return &ContractSet{
		Pool:           pool,
		UnitOfWork:     NewUnitOfWork(pool),
		Users:          NewUserStore(pool),
		Sessions:       NewSessionStore(pool),
		Permissions:    NewPermissionStore(pool),
		Config:         NewConfigStore(pool),
		Outbox:         NewEventOutbox(pool),
		Credentials:    NewCredentialStore(pool),
		Clock:          NewClock(),
		IDs:            NewIDGenerator(),
		Health:         NewHealthProbe(pool),
		HealthReporter: NewComponentHealthReporter(pool),
	}
}

// compile-time assertion that the module satisfies the built-in Module
// interface.
var _ builtin.Module = Module{}
