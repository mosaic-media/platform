package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

// queryer is the minimal query surface every store method depends on. Both
// *pgxpool.Pool (direct, non-transactional reads for the authentication and
// query paths) and pgx.Tx (transaction-scoped writes reached through a
// UnitOfWork) satisfy it, so a single store implementation serves both
// without knowing which handle it holds. This is how repository methods stay
// "written against transaction-scoped handles" (MEG-015 §05) while also
// backing the direct read path.
type queryer interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}
