package postgres_test

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/jackc/pgx/v5/pgxpool"
)

// This harness gives the Postgres adapter a REAL PostgreSQL instance to run
// against, satisfying MEG-015 §11's requirement that contract tests execute
// against real PostgreSQL (not a fake).
//
// Two ways to provide the database:
//   - Set MOSAIC_TEST_POSTGRES_DSN to point at an existing PostgreSQL (the
//     docker-compose path documented in README.md). The DSN's user must be
//     able to CREATE/DROP databases, as the migration tests create throwaway
//     databases.
//   - Otherwise the harness downloads and starts an embedded PostgreSQL for
//     the duration of the test binary (no Docker required). If that fails
//     (for example, no network on first run), the integration tests skip with
//     a clear reason rather than failing the whole `go test ./...`.

const (
	embeddedPort   = 5455
	embeddedUser   = "postgres"
	embeddedPass   = "postgres"
	embeddedBaseDB = "postgres" // maintenance DB for CREATE/DROP DATABASE
)

var (
	embedded      *embeddedpostgres.EmbeddedPostgres
	adminHostPort string // host:port for building DSNs
	pgUnavailable string // non-empty => integration tests skip with this reason
	dbCounter     atomic.Int64
)

func TestMain(m *testing.M) {
	code := func() int {
		if dsn := os.Getenv("MOSAIC_TEST_POSTGRES_DSN"); dsn != "" {
			hp, err := hostPortFromDSN(dsn)
			if err != nil {
				pgUnavailable = "MOSAIC_TEST_POSTGRES_DSN is set but unparseable: " + err.Error()
				return m.Run()
			}
			adminHostPort = hp
			return m.Run()
		}

		embedded = embeddedpostgres.NewDatabase(
			embeddedpostgres.DefaultConfig().
				Port(embeddedPort).
				Username(embeddedUser).
				Password(embeddedPass).
				Database(embeddedBaseDB).
				StartTimeout(90 * time.Second),
		)
		if err := embedded.Start(); err != nil {
			pgUnavailable = "embedded PostgreSQL could not start (set MOSAIC_TEST_POSTGRES_DSN to use an external database): " + err.Error()
			return m.Run()
		}
		adminHostPort = fmt.Sprintf("%s:%d", "localhost", embeddedPort)
		defer func() { _ = embedded.Stop() }()
		return m.Run()
	}()
	os.Exit(code)
}

// requirePostgres skips the calling test when no database is available.
func requirePostgres(t *testing.T) {
	t.Helper()
	if pgUnavailable != "" {
		t.Skip(pgUnavailable)
	}
}

func adminDSN() string {
	if dsn := os.Getenv("MOSAIC_TEST_POSTGRES_DSN"); dsn != "" {
		return dsn
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		embeddedUser, embeddedPass, adminHostPort, embeddedBaseDB)
}

func dsnForDatabase(name string) string {
	if dsn := os.Getenv("MOSAIC_TEST_POSTGRES_DSN"); dsn != "" {
		// Replace the database path segment of the external DSN.
		return replaceDatabase(dsn, name)
	}
	return fmt.Sprintf("postgres://%s:%s@%s/%s?sslmode=disable",
		embeddedUser, embeddedPass, adminHostPort, name)
}

// freshDatabase creates a uniquely-named, empty database and returns a pool
// connected to it. The database is dropped on test cleanup. Migration tests
// use this to exercise a truly empty schema; the contract suite uses one
// migrated database and truncates between subtests instead.
func freshDatabase(t *testing.T) *pgxpool.Pool {
	t.Helper()
	requirePostgres(t)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	name := fmt.Sprintf("mosaic_test_%d_%d", time.Now().UnixNano(), dbCounter.Add(1))

	admin, err := pgxpool.New(ctx, adminDSN())
	if err != nil {
		t.Fatalf("connect admin: %v", err)
	}
	if _, err := admin.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		admin.Close()
		t.Fatalf("create database %s: %v", name, err)
	}
	admin.Close()

	pool, err := pgxpool.New(ctx, dsnForDatabase(name))
	if err != nil {
		t.Fatalf("connect %s: %v", name, err)
	}

	t.Cleanup(func() {
		pool.Close()
		cctx, ccancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer ccancel()
		adminPool, err := pgxpool.New(cctx, adminDSN())
		if err != nil {
			return
		}
		defer adminPool.Close()
		// WITH (FORCE) terminates lingering connections (PostgreSQL 13+).
		_, _ = adminPool.Exec(cctx, "DROP DATABASE IF EXISTS "+name+" WITH (FORCE)")
	})

	return pool
}

// truncateAll clears every data table (everything except the migration
// tracking table) so the contract suite's subtests start from a clean slate
// while sharing one migrated database.
func truncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	rows, err := pool.Query(ctx,
		`SELECT tablename FROM pg_tables
		  WHERE schemaname = 'public' AND tablename <> 'platform_schema_migrations'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	if len(tables) == 0 {
		return
	}

	stmt := "TRUNCATE TABLE "
	for i, name := range tables {
		if i > 0 {
			stmt += ", "
		}
		stmt += `"` + name + `"`
	}
	stmt += " RESTART IDENTITY CASCADE"
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}
