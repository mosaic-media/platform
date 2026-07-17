// Package postgres is the built-in Postgres module: the PostgreSQL
// implementation of storage, migrations and outbox, registered through
// internal/composition/builtin the same way an external Module would be
// discovered (see CLAUDE.md package tier model). It arrives with the
// PostgreSQL adapter and migrations slice (MEG-015 §12).
package postgres
