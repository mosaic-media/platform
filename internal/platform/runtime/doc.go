// Package runtime builds the MEG-015 §10 Supervisor handoff surface:
// Generation metadata (metadata.go), a process Lifecycle (lifecycle.go),
// Readiness/Liveness health (readiness.go, liveness.go), migration status
// (migration.go), configuration activation status (config_activation.go),
// and a graceful Shutdown hook (shutdown.go).
//
// This package must not import internal/modules/postgres or any other
// Module (MEG-015 §02 — dependencies point inward): the composition root
// (cmd/mosaic-platform/main.go) is what bridges concrete Postgres/events
// values into these adapter-agnostic functions, the same way it already
// does for internal/platform/diagnostics.
package runtime
