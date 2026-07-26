// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// tokenStore is the PostgreSQL contracts.TokenStore.
//
// It takes a queryer rather than a pool, so the same implementation serves the
// direct read path (validating an access token on every call) and the
// transaction-scoped write path (issuing a pair alongside the session it
// belongs to).
type tokenStore struct {
	q queryer
}

// NewTokenStore builds a pool-backed TokenStore for the direct read path.
func NewTokenStore(pool *pgxpool.Pool) contracts.TokenStore {
	return &tokenStore{q: pool}
}

func (s *tokenStore) SaveAccess(ctx context.Context, token domain.AccessToken) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO session_access_tokens (token_hash, session_id, issued_at, expires_at)
		 VALUES ($1, $2, $3, $4)`,
		token.Hash, string(token.SessionID), token.IssuedAt.UTC(), token.ExpiresAt.UTC())
	if err != nil {
		return mapError("save access token", err)
	}
	return nil
}

func (s *tokenStore) FindAccess(ctx context.Context, hash string) (domain.AccessToken, error) {
	var (
		token     domain.AccessToken
		sessionID string
	)
	err := s.q.QueryRow(ctx,
		`SELECT token_hash, session_id, issued_at, expires_at
		   FROM session_access_tokens WHERE token_hash = $1`, hash).
		Scan(&token.Hash, &sessionID, &token.IssuedAt, &token.ExpiresAt)
	if err != nil {
		if isNoRows(err) {
			return domain.AccessToken{}, contracts.NewError(contracts.NotFound, "access token not found")
		}
		return domain.AccessToken{}, mapError("find access token", err)
	}
	token.SessionID = domain.SessionID(sessionID)
	return token, nil
}

func (s *tokenStore) SaveRefresh(ctx context.Context, token domain.RefreshToken) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO session_refresh_tokens
		   (token_hash, session_id, chain_id, device_id, issued_at, expires_at, used_at, revoked_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8)`,
		token.Hash, string(token.SessionID), string(token.ChainID), string(token.DeviceID),
		token.IssuedAt.UTC(), token.ExpiresAt.UTC(), token.UsedAt, token.RevokedAt)
	if err != nil {
		return mapError("save refresh token", err)
	}
	return nil
}

func (s *tokenStore) FindRefresh(ctx context.Context, hash string) (domain.RefreshToken, error) {
	var (
		token     domain.RefreshToken
		sessionID string
		chainID   string
		deviceID  string
	)
	err := s.q.QueryRow(ctx,
		`SELECT token_hash, session_id, chain_id, device_id, issued_at, expires_at, used_at, revoked_at
		   FROM session_refresh_tokens WHERE token_hash = $1`, hash).
		Scan(&token.Hash, &sessionID, &chainID, &deviceID,
			&token.IssuedAt, &token.ExpiresAt, &token.UsedAt, &token.RevokedAt)
	if err != nil {
		if isNoRows(err) {
			return domain.RefreshToken{}, contracts.NewError(contracts.NotFound, "refresh token not found")
		}
		return domain.RefreshToken{}, mapError("find refresh token", err)
	}
	token.SessionID = domain.SessionID(sessionID)
	token.ChainID = domain.ID(chainID)
	token.DeviceID = domain.DeviceID(deviceID)
	return token, nil
}

// MarkRefreshUsed spends a token, reporting whether this call is what spent it.
//
// One conditional UPDATE rather than a read and a write. Two refreshes arriving
// together would both read an unspent token and both proceed, handing out two
// live chains from one credential; here exactly one of them affects a row and
// the other learns it was second — which is the same signal a genuine replay
// produces, and is treated the same way.
func (s *tokenStore) MarkRefreshUsed(ctx context.Context, hash string, at time.Time) (bool, error) {
	tag, err := s.q.Exec(ctx,
		`UPDATE session_refresh_tokens SET used_at = $2
		  WHERE token_hash = $1 AND used_at IS NULL AND revoked_at IS NULL`,
		hash, at.UTC())
	if err != nil {
		return false, mapError("mark refresh token used", err)
	}
	return tag.RowsAffected() == 1, nil
}

func (s *tokenStore) RevokeChain(ctx context.Context, chainID domain.ID, at time.Time) error {
	if _, err := s.q.Exec(ctx,
		`UPDATE session_refresh_tokens SET revoked_at = $2
		  WHERE chain_id = $1 AND revoked_at IS NULL`,
		string(chainID), at.UTC()); err != nil {
		return mapError("revoke refresh chain", err)
	}
	// The access tokens of every session the chain touched go too. A revoked
	// chain that left a live access token would keep the thief working for the
	// rest of its lifetime, which is the one window the short lifetime exists
	// to bound and not one to leave open on purpose.
	if _, err := s.q.Exec(ctx,
		`DELETE FROM session_access_tokens
		  WHERE session_id IN (SELECT session_id FROM session_refresh_tokens WHERE chain_id = $1)`,
		string(chainID)); err != nil {
		return mapError("revoke chain access tokens", err)
	}
	return nil
}

func (s *tokenStore) RevokeSession(ctx context.Context, id domain.SessionID, at time.Time) error {
	if _, err := s.q.Exec(ctx,
		`UPDATE session_refresh_tokens SET revoked_at = $2
		  WHERE session_id = $1 AND revoked_at IS NULL`,
		string(id), at.UTC()); err != nil {
		return mapError("revoke session refresh tokens", err)
	}
	// Deleted rather than marked: an access token has no history worth keeping
	// and the table is the fastest-growing one in the schema.
	if _, err := s.q.Exec(ctx,
		`DELETE FROM session_access_tokens WHERE session_id = $1`, string(id)); err != nil {
		return mapError("revoke session access tokens", err)
	}
	return nil
}

func (s *tokenStore) DeleteExpired(ctx context.Context, before time.Time) (int, error) {
	cutoff := before.UTC()
	access, err := s.q.Exec(ctx, `DELETE FROM session_access_tokens WHERE expires_at < $1`, cutoff)
	if err != nil {
		return 0, mapError("delete expired access tokens", err)
	}
	refresh, err := s.q.Exec(ctx, `DELETE FROM session_refresh_tokens WHERE expires_at < $1`, cutoff)
	if err != nil {
		return int(access.RowsAffected()), mapError("delete expired refresh tokens", err)
	}
	return int(access.RowsAffected() + refresh.RowsAffected()), nil
}
