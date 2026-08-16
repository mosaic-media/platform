// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package postgres is the built-in PostgreSQL module: the mandatory first
// storage adapter. It implements the Platform storage contracts (UnitOfWork
// and the stores, plus Clock, IDGenerator and HealthProbe) against
// PostgreSQL via pgx, owns its embedded schema migrations, and is registered
// through internal/composition/builtin the same way a future external Module
// would be discovered.
//
// It owns SQL and row mapping and never lets a pgx row, SQLSTATE code or other
// driver internal escape: every error a store returns passes through mapError
// into one of the seven Platform error categories. It provides EventOutbox
// persistence; the worker that drains and publishes those rows is
// internal/platform/events.
package postgres
