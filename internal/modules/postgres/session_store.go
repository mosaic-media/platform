package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// sessionStore is the PostgreSQL contracts.SessionStore.
type sessionStore struct {
	q queryer
}

// NewSessionStore builds a pool-backed SessionStore for the direct read
// path used during authentication.
func NewSessionStore(pool *pgxpool.Pool) contracts.SessionStore {
	return &sessionStore{q: pool}
}

const sessionColumns = `id, user_id, device_id, issued_at, last_seen_at, expires_at, auth_strength, capabilities, revoked_at`

func (s *sessionStore) Create(ctx context.Context, session domain.Session) (domain.Session, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO sessions
		   (id, user_id, device_id, issued_at, last_seen_at, expires_at, auth_strength, capabilities, revoked_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		string(session.ID), string(session.UserID), string(session.DeviceID),
		session.IssuedAt, session.LastSeenAt, session.ExpiresAt,
		string(session.AuthStrength), permissionsToStrings(session.Capabilities), session.RevokedAt,
	)
	if err != nil {
		return domain.Session{}, mapError("create session", err)
	}
	return session, nil
}

func (s *sessionStore) FindByID(ctx context.Context, id domain.SessionID) (domain.Session, error) {
	row := s.q.QueryRow(ctx, `SELECT `+sessionColumns+` FROM sessions WHERE id = $1`, string(id))
	session, err := scanSession(row)
	if err != nil {
		if isNoRows(err) {
			return domain.Session{}, contracts.NewError(contracts.NotFound, "session not found")
		}
		return domain.Session{}, mapError("find session by id", err)
	}
	return session, nil
}

// Revoke marks the session revoked (the authoritative signal for
// Session.Revoked) and records the revocation in the audit table. Both
// writes run through the same queryer, so when Revoke is called inside a
// UnitOfWork they commit atomically with the rest of the command.
func (s *sessionStore) Revoke(ctx context.Context, id domain.SessionID) error {
	now := time.Now().UTC()
	tag, err := s.q.Exec(ctx,
		`UPDATE sessions SET revoked_at = $2 WHERE id = $1 AND revoked_at IS NULL`,
		string(id), now,
	)
	if err != nil {
		return mapError("revoke session", err)
	}
	if tag.RowsAffected() == 0 {
		// Either the session does not exist or it was already revoked. Confirm
		// which so callers get an accurate category.
		var exists bool
		if err := s.q.QueryRow(ctx, `SELECT true FROM sessions WHERE id = $1`, string(id)).Scan(&exists); err != nil {
			if isNoRows(err) {
				return contracts.NewError(contracts.NotFound, "session not found")
			}
			return mapError("confirm session for revoke", err)
		}
		// Already revoked: revocation is idempotent, so this is a success.
		return nil
	}

	if _, err := s.q.Exec(ctx,
		`INSERT INTO revoked_sessions (session_id, revoked_at, reason) VALUES ($1, $2, $3)
		 ON CONFLICT (session_id) DO NOTHING`,
		string(id), now, "",
	); err != nil {
		return mapError("record session revocation", err)
	}
	return nil
}

func scanSession(row pgx.Row) (domain.Session, error) {
	var (
		session      domain.Session
		id           string
		userID       string
		deviceID     string
		authStrength string
		capabilities []string
		revokedAt    *time.Time
	)
	if err := row.Scan(
		&id, &userID, &deviceID,
		&session.IssuedAt, &session.LastSeenAt, &session.ExpiresAt,
		&authStrength, &capabilities, &revokedAt,
	); err != nil {
		return domain.Session{}, err
	}
	session.ID = domain.SessionID(id)
	session.UserID = domain.UserID(userID)
	session.DeviceID = domain.DeviceID(deviceID)
	session.AuthStrength = domain.AuthStrength(authStrength)
	session.Capabilities = stringsToPermissions(capabilities)
	session.RevokedAt = revokedAt
	return session, nil
}
