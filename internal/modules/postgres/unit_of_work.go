// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// UnitOfWork is the PostgreSQL implementation of contracts.UnitOfWork. It
// owns transaction mechanics; application services decide transaction scope.
type UnitOfWork struct {
	pool *pgxpool.Pool
}

// NewUnitOfWork builds a UnitOfWork over pool.
func NewUnitOfWork(pool *pgxpool.Pool) *UnitOfWork {
	return &UnitOfWork{pool: pool}
}

// WithinTx opens a single database transaction, hands fn a Tx whose every
// store is bound to that same transaction handle, and commits only if fn
// returns nil. Because Users(), Sessions(), Outbox() and the rest all share
// the one pgx.Tx, a state write and its outbox event are structurally in the
// same transaction — atomicity is enforced by construction, not convention.
// Any error from fn rolls the whole transaction back, so a partial write is
// never committed and never observable by another transaction.
func (u *UnitOfWork) WithinTx(ctx context.Context, fn func(ctx context.Context, tx contracts.Tx) error) error {
	// One span per transaction (ADR 0055, seam 5), and the grouping layer of
	// the whole waterfall: the per-statement spans the pool's tracer emits
	// (seam 6) nest inside this one, so a trace shows "this write took 40ms,
	// and here are the six statements it took to do it" rather than six
	// statements floating loose beside the handler that issued them.
	//
	// It also makes rollback visible. A transaction that rolled back is
	// otherwise indistinguishable from one that committed, and "the write
	// silently did not happen" is among the harder failures to chase.
	ctx, span := telemetry.Start(ctx, "tx", telemetry.String("db.system", "postgresql"))
	defer span.End()

	pgxTx, err := u.pool.Begin(ctx)
	if err != nil {
		wrapped := mapError("begin transaction", err)
		span.Fail(string(contracts.CategoryOf(wrapped)), wrapped)
		return wrapped
	}

	// Rollback is a no-op once Commit has succeeded; if fn panics or returns
	// early, this guarantees the transaction is not left open.
	committed := false
	defer func() {
		if !committed {
			_ = pgxTx.Rollback(ctx)
		}
	}()

	if err := fn(ctx, &tx{q: pgxTx}); err != nil {
		// fn's error is already a Platform error (the stores mapped it); do
		// not re-wrap. The deferred rollback discards the partial work.
		span.SetAttributes(telemetry.String("db.outcome", "rollback"))
		span.Fail(string(contracts.CategoryOf(err)), err)
		return err
	}

	if err := pgxTx.Commit(ctx); err != nil {
		wrapped := mapError("commit transaction", err)
		span.SetAttributes(telemetry.String("db.outcome", "rollback"))
		span.Fail(string(contracts.CategoryOf(wrapped)), wrapped)
		return wrapped
	}
	committed = true
	span.SetAttributes(telemetry.String("db.outcome", "commit"))
	return nil
}

type tx struct {
	q pgx.Tx
}

func (t *tx) Users() contracts.UserStore             { return &userStore{q: t.q} }
func (t *tx) Sessions() contracts.SessionStore       { return &sessionStore{q: t.q} }
func (t *tx) Permissions() contracts.PermissionStore { return &permissionStore{q: t.q} }
func (t *tx) Config() contracts.ConfigStore          { return &configStore{q: t.q} }
func (t *tx) Outbox() contracts.EventOutbox          { return &eventOutbox{q: t.q} }
func (t *tx) UserPreferences() contracts.UserPreferenceStore {
	return &userPreferenceStore{q: t.q}
}
func (t *tx) Credentials() contracts.CredentialStore { return &credentialStore{q: t.q} }

// The content model (ADR 0013). These share the same pgx.Tx as every store
// above, so a node, its parts and the outbox event announcing it commit
// atomically or not at all.
func (t *tx) Nodes() contracts.NodeStore         { return &nodeStore{q: t.q} }
func (t *tx) Parts() contracts.PartStore         { return &partStore{q: t.q} }
func (t *tx) Relations() contracts.RelationStore { return &relationStore{q: t.q} }
func (t *tx) SourceBindings() contracts.SourceBindingStore {
	return &sourceBindingStore{q: t.q}
}

// PlaybackStates joins the set (ADR 0046) so a position change and its outbox
// event share the one transaction, like every other content write.
func (t *tx) PlaybackStates() contracts.PlaybackStateStore {
	return &playbackStateStore{q: t.q}
}

// ModuleSettings joins the set (ADR 0021) so a module's settings change and
// its outbox event share the one transaction.
func (t *tx) ModuleSettings() contracts.ModuleSettingsStore {
	return &moduleSettingsStore{q: t.q}
}
