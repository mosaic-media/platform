// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import "context"

// ctxKey is unexported so nothing outside this package can seed or replace the
// logger by constructing the key itself.
type ctxKey struct{}

// Into returns a context carrying l. Edges call it: a transport handler binds
// the request scope it knows about, a job binds its own, the composition root
// binds the process-wide logger once at startup.
//
// A nil logger is ignored rather than stored, so a caller that has not built
// one yet cannot accidentally blank out a logger an outer scope established.
func Into(ctx context.Context, l *Logger) context.Context {
	if l == nil {
		return ctx
	}
	return context.WithValue(ctx, ctxKey{}, l)
}

// From returns the Logger ctx carries, or a working no-op if it carries none.
//
// It never returns nil and never panics. A call site is meant to read
// telemetry.From(ctx).Info(…) with no nil check and no error handling — that
// is the ergonomic claim ADR 0053 makes, and it only holds if the unseeded
// case is silent rather than fatal. A library path, a test, or a goroutine
// started from context.Background() degrades to writing nothing.
func From(ctx context.Context) *Logger {
	if ctx == nil {
		return nop
	}
	if l, ok := ctx.Value(ctxKey{}).(*Logger); ok && l != nil {
		return l
	}
	return nop
}

// With is shorthand for seeding a context with additional bound fields — the
// common shape at an edge that already has a logger in ctx and wants to add
// request scope to it.
func With(ctx context.Context, fields ...Field) context.Context {
	return Into(ctx, From(ctx).With(fields...))
}

// For is shorthand for attributing a context's logger to a component.
func For(ctx context.Context, component string) context.Context {
	return Into(ctx, From(ctx).For(component))
}
