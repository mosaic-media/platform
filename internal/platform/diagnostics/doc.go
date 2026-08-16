// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package diagnostics aggregates real component health (registry.go) and
// builds an anonymised support bundle (support_bundle.go).
//
// Structured logging and the redaction vocabulary live in
// internal/platform/telemetry (platform#31), not here, so the support bundle
// and the module surface across the SDK boundary share one fail-closed
// classification instead of two that can drift.
//
// This package must not import internal/modules/postgres or any other
// Module, per the inward dependency rule: it depends only on
// contracts.ComponentHealthReporter and domain.ComponentHealth, so it can
// aggregate real components' health without knowing what any of them are.
// The composition root (main.go, or a Module's own tests) is what wires a
// concrete reporter — e.g. internal/modules/postgres's — into a Registry.
package diagnostics
