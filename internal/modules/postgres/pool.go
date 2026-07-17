package postgres

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// Connect opens a connection pool to PostgreSQL using a libpq-style DSN or
// URL (for example "postgres://user:pass@host:5432/db?sslmode=disable") and
// verifies it is reachable. A failure to parse, connect or ping is reported
// as Unavailable — the storage dependency is not currently usable
// (MEG-015 §03).
//
// The pool uses PostgreSQL's default transaction isolation (Read Committed).
// Per MEG-007 §03, capabilities do not implement their own concurrency
// control; the database provides it, and uniqueness/serialization conflicts
// surface as SQLSTATE codes that mapError translates to Platform categories.
func Connect(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, contracts.WrapError(contracts.InvalidArgument, "parse postgres dsn", err)
	}

	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, contracts.WrapError(contracts.Unavailable, "open postgres pool", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, contracts.WrapError(contracts.Unavailable, "ping postgres", err)
	}

	return pool, nil
}
