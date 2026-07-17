package runtime

import "sync"

// MigrationPhase is where database migration stands relative to this
// process's boot (MEG-015 §10 — Migration status: "required, running,
// complete or failed").
type MigrationPhase string

const (
	// MigrationRequired means migration has not yet been attempted this
	// boot (the initial state — accurate whether or not any migrations are
	// actually pending, since that has not been checked yet).
	MigrationRequired MigrationPhase = "required"
	// MigrationRunning means a migration attempt is currently in flight.
	MigrationRunning MigrationPhase = "running"
	// MigrationComplete means the most recent migration attempt succeeded.
	MigrationComplete MigrationPhase = "complete"
	// MigrationFailed means the most recent migration attempt failed —
	// missing, incompatible (checksum mismatch), partially applied, or
	// database-ahead (MEG-007 §10), reported via Detail.
	MigrationFailed MigrationPhase = "failed"
)

// MigrationStatus is the Supervisor-facing migration state.
type MigrationStatus struct {
	Phase  MigrationPhase
	Detail string
}

// MigrationTracker tracks real migration phase transitions across the
// (synchronous) migration call the composition root makes, so a
// concurrent status check made while a slow migration is running observes
// Running rather than a stale pre-migration snapshot. It holds no
// PostgreSQL dependency itself — the composition root calls Begin before
// its own postgres.Migrate call and Complete with that call's result,
// keeping this package adapter-agnostic (MEG-015 §02).
type MigrationTracker struct {
	mu     sync.Mutex
	phase  MigrationPhase
	detail string
}

// NewMigrationTracker returns a MigrationTracker starting in
// MigrationRequired — migration has not been attempted yet.
func NewMigrationTracker() *MigrationTracker {
	return &MigrationTracker{phase: MigrationRequired}
}

// Begin records that a migration attempt has started.
func (t *MigrationTracker) Begin() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phase = MigrationRunning
	t.detail = ""
}

// Complete records a migration attempt's outcome: err == nil means it
// completed successfully; a non-nil err (the postgres package's fail-fast
// migration error) means it failed, with err's message preserved as
// Detail so an operator can see why.
func (t *MigrationTracker) Complete(err error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if err != nil {
		t.phase = MigrationFailed
		t.detail = err.Error()
		return
	}
	t.phase = MigrationComplete
	t.detail = ""
}

// Status returns the current migration status.
func (t *MigrationTracker) Status() MigrationStatus {
	t.mu.Lock()
	defer t.mu.Unlock()
	return MigrationStatus{Phase: t.phase, Detail: t.detail}
}
