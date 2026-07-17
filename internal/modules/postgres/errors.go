package postgres

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// mapError translates a pgx/PostgreSQL error into a Platform contract error
// (MEG-015 §03, §05 — Repository Implementation). It is the adapter
// boundary's error gate: every value a store method returns to application
// services passes through here, so a raw *pgconn.PgError, SQLSTATE code or
// pgx sentinel never leaks past the module. It always returns a
// *contracts.Error (or nil), never a driver error.
//
// message is a caller-supplied description of the operation that failed
// (MEG-001 §08 — Add Context); it never contains SQL or driver detail.
func mapError(message string, err error) error {
	if err == nil {
		return nil
	}

	// A pgx no-rows result is the driver's "not found"; callers that want a
	// resource-specific NotFound should check with isNoRows before calling
	// mapError, but mapping it here as a fallback keeps the guarantee that
	// no driver sentinel escapes.
	if errors.Is(err, pgx.ErrNoRows) {
		return contracts.WrapError(contracts.NotFound, message, err)
	}

	// Context cancellation/deadline while talking to the database means the
	// dependency was not usable within the caller's bounds.
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return contracts.WrapError(contracts.Unavailable, message, err)
	}

	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return contracts.WrapError(categoryForSQLState(pgErr.Code), message, err)
	}

	// Anything else — including connection-pool acquisition failures that are
	// not PgErrors — is an unexpected adapter failure.
	return contracts.WrapError(contracts.Internal, message, err)
}

// categoryForSQLState maps a PostgreSQL SQLSTATE code to a Platform error
// category. Codes are grouped by class per the PostgreSQL error-code
// appendix; only the classes the Platform distinguishes are enumerated,
// everything else falls through to Internal.
func categoryForSQLState(code string) contracts.ErrorCategory {
	switch code {
	// Class 23 — integrity constraint violation.
	case "23505": // unique_violation
		return contracts.Conflict
	case "23503": // foreign_key_violation
		return contracts.Conflict
	case "23514", // check_violation
		"23502", // not_null_violation
		"23P01": // exclusion_violation
		return contracts.InvalidArgument

	// Class 40 — transaction rollback (concurrency conflicts).
	case "40001", // serialization_failure
		"40P01": // deadlock_detected
		return contracts.Conflict

	// Class 22 — data exception (bad input values).
	case "22P02", // invalid_text_representation
		"22001", // string_data_right_truncation
		"22003": // numeric_value_out_of_range
		return contracts.InvalidArgument

	// Class 53 — insufficient resources; Class 57 — operator intervention;
	// Class 08 — connection exception. The dependency is not currently usable.
	case "53300", // too_many_connections
		"53400", // configuration_limit_exceeded
		"57P01", // admin_shutdown
		"57P03", // cannot_connect_now
		"08000", // connection_exception
		"08003", // connection_does_not_exist
		"08006": // connection_failure
		return contracts.Unavailable

	default:
		return contracts.Internal
	}
}

// isNoRows reports whether err is pgx's no-rows sentinel, so store methods
// can translate it into a resource-specific NotFound with their own message.
func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
