package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// credentialStore is the PostgreSQL contracts.CredentialStore, covering the
// local identity factors from MEG-015 §07 — password, passkey and recovery.
type credentialStore struct {
	q queryer
}

// NewCredentialStore builds a pool-backed CredentialStore for the direct
// read path used during authentication.
func NewCredentialStore(pool *pgxpool.Pool) contracts.CredentialStore {
	return &credentialStore{q: pool}
}

func (s *credentialStore) SavePassword(ctx context.Context, credential domain.PasswordCredential) error {
	// Upsert: a user has at most one password verifier; re-saving replaces it
	// (for example after a password change).
	_, err := s.q.Exec(ctx,
		`INSERT INTO password_credentials (user_id, hash, updated_at)
		 VALUES ($1, $2, $3)
		 ON CONFLICT (user_id) DO UPDATE SET hash = EXCLUDED.hash, updated_at = EXCLUDED.updated_at`,
		string(credential.UserID), credential.Hash, credential.UpdatedAt,
	)
	if err != nil {
		return mapError("save password credential", err)
	}
	return nil
}

func (s *credentialStore) FindPassword(ctx context.Context, userID domain.UserID) (domain.PasswordCredential, error) {
	row := s.q.QueryRow(ctx,
		`SELECT user_id, hash, updated_at FROM password_credentials WHERE user_id = $1`,
		string(userID),
	)
	var (
		credential domain.PasswordCredential
		uid        string
	)
	if err := row.Scan(&uid, &credential.Hash, &credential.UpdatedAt); err != nil {
		if isNoRows(err) {
			return domain.PasswordCredential{}, contracts.NewError(contracts.NotFound, "password credential not found")
		}
		return domain.PasswordCredential{}, mapError("find password credential", err)
	}
	credential.UserID = domain.UserID(uid)
	return credential, nil
}

func (s *credentialStore) SavePasskey(ctx context.Context, credential domain.PasskeyCredential) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO passkey_credentials (credential_id, user_id, public_key, created_at)
		 VALUES ($1, $2, $3, $4)`,
		credential.CredentialID, string(credential.UserID), credential.PublicKey, credential.CreatedAt,
	)
	if err != nil {
		return mapError("save passkey credential", err)
	}
	return nil
}

func (s *credentialStore) ListPasskeys(ctx context.Context, userID domain.UserID) ([]domain.PasskeyCredential, error) {
	rows, err := s.q.Query(ctx,
		`SELECT credential_id, user_id, public_key, created_at
		   FROM passkey_credentials WHERE user_id = $1 ORDER BY created_at`,
		string(userID),
	)
	if err != nil {
		return nil, mapError("list passkey credentials", err)
	}
	defer rows.Close()

	var passkeys []domain.PasskeyCredential
	for rows.Next() {
		var (
			passkey domain.PasskeyCredential
			uid     string
		)
		if err := rows.Scan(&passkey.CredentialID, &uid, &passkey.PublicKey, &passkey.CreatedAt); err != nil {
			return nil, mapError("scan passkey credential", err)
		}
		passkey.UserID = domain.UserID(uid)
		passkeys = append(passkeys, passkey)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("iterate passkey credentials", err)
	}
	return passkeys, nil
}

func (s *credentialStore) SaveRecoveryFactor(ctx context.Context, factor domain.RecoveryFactor) error {
	_, err := s.q.Exec(ctx,
		`INSERT INTO recovery_factors (user_id, code_hash, created_at, consumed_at)
		 VALUES ($1, $2, $3, $4)`,
		string(factor.UserID), factor.CodeHash, factor.CreatedAt, factor.ConsumedAt,
	)
	if err != nil {
		return mapError("save recovery factor", err)
	}
	return nil
}

// ConsumeRecoveryFactor marks a single unused recovery factor consumed and
// returns it. It fails NotFound if the code does not exist or was already
// consumed, so a recovery code can be spent at most once (MEG-009 §03).
func (s *credentialStore) ConsumeRecoveryFactor(ctx context.Context, userID domain.UserID, codeHash string) (domain.RecoveryFactor, error) {
	now := time.Now().UTC()
	row := s.q.QueryRow(ctx,
		`UPDATE recovery_factors SET consumed_at = $3
		  WHERE user_id = $1 AND code_hash = $2 AND consumed_at IS NULL
		  RETURNING user_id, code_hash, created_at, consumed_at`,
		string(userID), codeHash, now,
	)
	factor, err := scanRecoveryFactor(row)
	if err != nil {
		if isNoRows(err) {
			return domain.RecoveryFactor{}, contracts.NewError(contracts.NotFound, "recovery factor not found or already consumed")
		}
		return domain.RecoveryFactor{}, mapError("consume recovery factor", err)
	}
	return factor, nil
}

func scanRecoveryFactor(row pgx.Row) (domain.RecoveryFactor, error) {
	var (
		factor     domain.RecoveryFactor
		uid        string
		consumedAt *time.Time
	)
	if err := row.Scan(&uid, &factor.CodeHash, &factor.CreatedAt, &consumedAt); err != nil {
		return domain.RecoveryFactor{}, err
	}
	factor.UserID = domain.UserID(uid)
	factor.ConsumedAt = consumedAt
	return factor, nil
}
