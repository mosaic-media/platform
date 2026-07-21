// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"fmt"
	"io/fs"
	"sort"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

//go:embed migrations/*.sql
var migrationFiles embed.FS

// migrationsTable tracks which migrations have been applied, with the
// checksum of the exact SQL that was applied. It is the Platform's schema
// version record.
const migrationsTable = "platform_schema_migrations"

// migration is one embedded, versioned schema change.
type migration struct {
	Version  int
	Name     string
	SQL      string
	Checksum string
}

// MigrationStatus summarises the schema state for diagnostics and startup
// reporting.
type MigrationStatus struct {
	// AppliedVersion is the highest migration version recorded as applied,
	// or 0 for an empty database.
	AppliedVersion int
	// LatestVersion is the highest migration version this binary embeds.
	LatestVersion int
	// Pending is the number of embedded migrations not yet applied.
	Pending int
}

// FullyMigrated reports whether the database is at the version this binary
// expects.
func (s MigrationStatus) FullyMigrated() bool {
	return s.Pending == 0 && s.AppliedVersion == s.LatestVersion
}

// loadMigrations reads, parses, checksums and orders the embedded migration
// files. It fails if the embedded set is itself malformed — a non-numeric
// prefix, a duplicate version, or a gap — so a packaging mistake surfaces
// as a clear build/startup error rather than a silent skip.
func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		version, name, err := parseMigrationName(entry.Name())
		if err != nil {
			return nil, err
		}
		body, err := migrationFiles.ReadFile("migrations/" + entry.Name())
		if err != nil {
			return nil, fmt.Errorf("read migration %q: %w", entry.Name(), err)
		}
		// Normalise line endings before checksumming and executing so the
		// checksum is stable regardless of how the file was checked out
		// (git autocrlf turns LF into CRLF on Windows). Otherwise the same
		// migration would carry a different checksum per platform and a
		// cross-platform database would false-trigger the incompatible-schema
		// guard.
		normalized := strings.ReplaceAll(string(body), "\r\n", "\n")
		sum := sha256.Sum256([]byte(normalized))
		migrations = append(migrations, migration{
			Version:  version,
			Name:     name,
			SQL:      normalized,
			Checksum: hex.EncodeToString(sum[:]),
		})
	}

	sort.Slice(migrations, func(i, j int) bool {
		return migrations[i].Version < migrations[j].Version
	})

	// The embedded set must be a contiguous sequence 1..N with no duplicates.
	for i, m := range migrations {
		want := i + 1
		if m.Version != want {
			return nil, fmt.Errorf("embedded migrations are not contiguous: expected version %d, found %d (%s)", want, m.Version, m.Name)
		}
	}

	return migrations, nil
}

func parseMigrationName(filename string) (int, string, error) {
	base := strings.TrimSuffix(filename, ".sql")
	prefix, name, found := strings.Cut(base, "_")
	if !found {
		return 0, "", fmt.Errorf("migration %q must be named NNNN_name.sql", filename)
	}
	version, err := strconv.Atoi(prefix)
	if err != nil {
		return 0, "", fmt.Errorf("migration %q has a non-numeric version prefix: %w", filename, err)
	}
	if version < 1 {
		return 0, "", fmt.Errorf("migration %q version must be >= 1", filename)
	}
	return version, name, nil
}

// Migrate brings the database schema up to the version this binary embeds,
// and — before applying anything — validates that whatever is already
// applied is compatible with this binary. It is the migration gate: startup
// validates storage versions before execution and detects missing,
// incompatible or partially applied migrations.
//
// It fails fast, without applying anything, when it detects:
//   - incompatible: an applied version whose checksum differs from this
//     binary's migration of the same version (the schema was built from a
//     different definition);
//   - database-ahead: an applied version this binary does not know about
//     (the database was migrated by a newer binary); or
//   - partially applied: a gap in the applied version sequence (an earlier
//     version is missing while a later one is present).
//
// Each pending migration is applied in its own transaction together with its
// tracking row, so an interrupted run can never leave a migration's DDL
// applied without its record — there is no partial-within-a-version state.
func Migrate(ctx context.Context, pool *pgxpool.Pool) error {
	migrations, err := loadMigrations()
	if err != nil {
		return contracts.WrapError(contracts.Internal, "load migrations", err)
	}

	if err := ensureMigrationsTable(ctx, pool); err != nil {
		return err
	}

	applied, err := loadAppliedMigrations(ctx, pool)
	if err != nil {
		return err
	}

	if err := validateApplied(migrations, applied); err != nil {
		return err
	}

	for _, m := range migrations {
		if _, done := applied[m.Version]; done {
			continue
		}
		if err := applyMigration(ctx, pool, m); err != nil {
			return err
		}
	}

	return nil
}

// VerifyMigrated returns an error if the database is not fully migrated to
// this binary's latest version, without applying anything. It lets callers
// assert schema readiness (for example a readiness probe) separately from
// performing the migration.
func VerifyMigrated(ctx context.Context, pool *pgxpool.Pool) error {
	status, err := Status(ctx, pool)
	if err != nil {
		return err
	}
	if !status.FullyMigrated() {
		return contracts.NewError(contracts.Unavailable,
			fmt.Sprintf("database schema is not fully migrated: at version %d, expected %d (%d pending)",
				status.AppliedVersion, status.LatestVersion, status.Pending))
	}
	return nil
}

// Status reports the current schema state. It also runs the same
// compatibility validation as Migrate, so a database that is incompatible or
// ahead of this binary surfaces as an error here too rather than a
// misleading "healthy" status.
func Status(ctx context.Context, pool *pgxpool.Pool) (MigrationStatus, error) {
	migrations, err := loadMigrations()
	if err != nil {
		return MigrationStatus{}, contracts.WrapError(contracts.Internal, "load migrations", err)
	}

	applied, err := loadAppliedMigrations(ctx, pool)
	if err != nil {
		return MigrationStatus{}, err
	}
	if err := validateApplied(migrations, applied); err != nil {
		return MigrationStatus{}, err
	}

	latest := migrations[len(migrations)-1].Version
	appliedVersion := 0
	pending := 0
	for _, m := range migrations {
		if _, done := applied[m.Version]; done {
			if m.Version > appliedVersion {
				appliedVersion = m.Version
			}
		} else {
			pending++
		}
	}

	return MigrationStatus{
		AppliedVersion: appliedVersion,
		LatestVersion:  latest,
		Pending:        pending,
	}, nil
}

func ensureMigrationsTable(ctx context.Context, pool *pgxpool.Pool) error {
	_, err := pool.Exec(ctx, fmt.Sprintf(`
CREATE TABLE IF NOT EXISTS %s (
    version    integer     PRIMARY KEY,
    name       text        NOT NULL,
    checksum   text        NOT NULL,
    applied_at timestamptz NOT NULL DEFAULT now()
)`, migrationsTable))
	if err != nil {
		return mapError("ensure migrations table", err)
	}
	return nil
}

type appliedMigration struct {
	Name     string
	Checksum string
}

func loadAppliedMigrations(ctx context.Context, pool *pgxpool.Pool) (map[int]appliedMigration, error) {
	// A missing tracking table means nothing has been applied yet (an empty
	// database). Read-only callers — Status, VerifyMigrated, the health probe
	// — must report that as "0 applied / all pending" rather than erroring, so
	// only Migrate itself ever creates the table.
	var present bool
	if err := pool.QueryRow(ctx,
		`SELECT to_regclass($1) IS NOT NULL`, "public."+migrationsTable,
	).Scan(&present); err != nil {
		return nil, mapError("check migrations table", err)
	}
	if !present {
		return map[int]appliedMigration{}, nil
	}

	rows, err := pool.Query(ctx, fmt.Sprintf(`SELECT version, name, checksum FROM %s`, migrationsTable))
	if err != nil {
		return nil, mapError("query applied migrations", err)
	}
	defer rows.Close()

	applied := make(map[int]appliedMigration)
	for rows.Next() {
		var version int
		var rec appliedMigration
		if err := rows.Scan(&version, &rec.Name, &rec.Checksum); err != nil {
			return nil, mapError("scan applied migration", err)
		}
		applied[version] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate applied migrations", err)
	}
	return applied, nil
}

// validateApplied checks that everything already recorded as applied is
// compatible with this binary's embedded migration set. See Migrate for the
// three failure conditions it enforces.
func validateApplied(migrations []migration, applied map[int]appliedMigration) error {
	known := make(map[int]migration, len(migrations))
	for _, m := range migrations {
		known[m.Version] = m
	}

	// Incompatible / database-ahead checks.
	highestApplied := 0
	for version, rec := range applied {
		expected, ok := known[version]
		if !ok {
			return contracts.NewError(contracts.Unavailable,
				fmt.Sprintf("database schema is ahead of this binary: applied migration version %d (%s) is unknown; upgrade the Platform binary", version, rec.Name))
		}
		if rec.Checksum != expected.Checksum {
			return contracts.NewError(contracts.Unavailable,
				fmt.Sprintf("incompatible migration %d (%s): applied checksum does not match this binary's definition; the schema was built from a different migration", version, expected.Name))
		}
		if version > highestApplied {
			highestApplied = version
		}
	}

	// Partially-applied check: applied versions must form a contiguous prefix
	// 1..highestApplied. A gap means an earlier migration is missing while a
	// later one is present.
	for v := 1; v <= highestApplied; v++ {
		if _, ok := applied[v]; !ok {
			return contracts.NewError(contracts.Unavailable,
				fmt.Sprintf("partially applied migrations detected: version %d is missing but a later version is present; the database is in an inconsistent state", v))
		}
	}

	return nil
}

// applyMigration runs one migration's DDL and records its tracking row in a
// single transaction, so the two either commit together or not at all.
func applyMigration(ctx context.Context, pool *pgxpool.Pool, m migration) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return mapError(fmt.Sprintf("begin migration %d", m.Version), err)
	}
	defer func() { _ = tx.Rollback(ctx) }() // no-op after a successful commit

	// No arguments: pgx uses the simple query protocol, which executes the
	// migration file's multiple statements in one round trip.
	if _, err := tx.Exec(ctx, m.SQL); err != nil {
		return mapError(fmt.Sprintf("apply migration %d (%s)", m.Version, m.Name), err)
	}

	if _, err := tx.Exec(ctx,
		fmt.Sprintf(`INSERT INTO %s (version, name, checksum) VALUES ($1, $2, $3)`, migrationsTable),
		m.Version, m.Name, m.Checksum,
	); err != nil {
		return mapError(fmt.Sprintf("record migration %d", m.Version), err)
	}

	if err := tx.Commit(ctx); err != nil {
		return mapError(fmt.Sprintf("commit migration %d", m.Version), err)
	}
	return nil
}
