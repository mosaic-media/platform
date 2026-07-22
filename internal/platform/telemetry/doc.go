// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package telemetry is the Platform's structured observation surface: the
// redaction-classed field vocabulary, a levelled logger, the sinks it writes
// to, and the process identity every record carries.
//
// It is ambient (ADR 0053). Nothing takes a Logger as a constructor
// parameter; an edge binds what it knows with Into and everything downstream
// reaches it with From. That is what makes one line at a call site carry full
// correlation, and it is why this package exists rather than a logger being
// threaded through the object graph:
//
//	telemetry.From(ctx).Info("stream closed", telemetry.Duration("elapsed", d))
//
// From on a context that was never seeded returns a working no-op. A logger
// that can panic is worse than no logger, and a library path or a test that
// forgot to seed must degrade quietly rather than crash a caller.
//
// The dependency rule is unchanged: internal/platform/domain must never
// import this package. The domain applies rules and returns results; it does
// not narrate. Application services, transports, modules and the composition
// root are the layers that observe.
//
// This package holds the redaction vocabulary that internal/platform/diagnostics
// used to own, because more than one thing needs it now — the support bundle
// still, and the module surface across the SDK boundary (ADR 0059) — and one
// fail-closed classification is worth more than two that can drift apart.
package telemetry
