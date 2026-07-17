// Package diagnostics aggregates real component health (registry.go),
// writes redacted structured local logs (logger.go), and builds an
// anonymised support bundle (support_bundle.go) — MEG-015 §09's
// Diagnostics Model, Local Logs and Support bundle sections, and MEG-015
// §12's Diagnostics and health slice.
//
// This package must not import internal/modules/postgres or any other
// Module (MEG-015 §02 — dependencies point inward): it depends only on
// contracts.ComponentHealthReporter and domain.ComponentHealth, so it can
// aggregate real components' health without knowing what any of them are.
// The composition root (main.go, or a Module's own tests) is what wires a
// concrete reporter — e.g. internal/modules/postgres's — into a Registry.
package diagnostics
