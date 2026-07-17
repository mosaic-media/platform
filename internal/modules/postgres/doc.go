// Package postgres is the built-in PostgreSQL module: the mandatory first
// storage adapter (MEG-015 §05). It implements the Platform storage
// contracts (UnitOfWork and the stores, plus Clock, IDGenerator and
// HealthProbe) against PostgreSQL via pgx, owns its embedded schema
// migrations, and is registered through internal/composition/builtin the
// same way a future external Module would be discovered (see CLAUDE.md
// package tier model).
//
// It owns SQL and row mapping and never lets a pgx row, SQLSTATE code or
// other driver internal escape: every error a store returns passes through
// mapError into one of the seven Platform error categories (MEG-015 §03).
// The outbox worker and event publishing are a later slice; this module
// provides EventOutbox persistence only.
package postgres
