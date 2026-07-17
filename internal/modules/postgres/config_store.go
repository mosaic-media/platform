package postgres

import (
	"context"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// configStore is the PostgreSQL contracts.ConfigStore. Persistence and
// status bookkeeping only: the activation state machine itself lives in
// internal/platform/config (MEG-015 §08).
type configStore struct {
	q queryer
}

// NewConfigStore builds a pool-backed ConfigStore.
func NewConfigStore(pool *pgxpool.Pool) contracts.ConfigStore {
	return &configStore{q: pool}
}

const configColumns = `id, payload, status, created_at, validated_at, validation_detail, activated_at, rejected_at, superseded_at`

func (s *configStore) Save(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO config_versions
		   (id, payload, status, created_at, validated_at, validation_detail, activated_at, rejected_at, superseded_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		string(version.ID), version.Payload, string(version.Status), version.CreatedAt,
		version.ValidatedAt, version.ValidationDetail, version.ActivatedAt, version.RejectedAt, version.SupersededAt,
	)
	if err != nil {
		return domain.ConfigVersion{}, mapError("save config version", err)
	}
	return version, nil
}

func (s *configStore) Latest(ctx context.Context) (domain.ConfigVersion, error) {
	row := s.q.QueryRow(ctx,
		`SELECT `+configColumns+` FROM config_versions ORDER BY created_at DESC, id DESC LIMIT 1`,
	)
	version, err := scanConfigVersion(row)
	if err != nil {
		if isNoRows(err) {
			return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no config version")
		}
		return domain.ConfigVersion{}, mapError("find latest config version", err)
	}
	return version, nil
}

func (s *configStore) FindByID(ctx context.Context, id domain.ConfigVersionID) (domain.ConfigVersion, error) {
	row := s.q.QueryRow(ctx, `SELECT `+configColumns+` FROM config_versions WHERE id = $1`, string(id))
	version, err := scanConfigVersion(row)
	if err != nil {
		if isNoRows(err) {
			return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
		}
		return domain.ConfigVersion{}, mapError("find config version by id", err)
	}
	return version, nil
}

func (s *configStore) FindActive(ctx context.Context) (domain.ConfigVersion, error) {
	row := s.q.QueryRow(ctx,
		`SELECT `+configColumns+` FROM config_versions WHERE status = 'active' ORDER BY activated_at DESC LIMIT 1`,
	)
	version, err := scanConfigVersion(row)
	if err != nil {
		if isNoRows(err) {
			return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "no active config version")
		}
		return domain.ConfigVersion{}, mapError("find active config version", err)
	}
	return version, nil
}

// UpdateStatus overwrites the mutable transition fields of an existing
// config version. A unique index on (status) WHERE status = 'active'
// (migration 0010) rejects a second concurrent activation with Conflict,
// so at most one version can ever be Active even under a racing caller.
func (s *configStore) UpdateStatus(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	tag, err := s.q.Exec(ctx,
		`UPDATE config_versions
		 SET status = $2, validated_at = $3, validation_detail = $4, activated_at = $5, rejected_at = $6, superseded_at = $7
		 WHERE id = $1`,
		string(version.ID), string(version.Status), version.ValidatedAt, version.ValidationDetail,
		version.ActivatedAt, version.RejectedAt, version.SupersededAt,
	)
	if err != nil {
		return domain.ConfigVersion{}, mapError("update config version status", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.ConfigVersion{}, contracts.NewError(contracts.NotFound, "config version not found")
	}
	return version, nil
}

func scanConfigVersion(row pgx.Row) (domain.ConfigVersion, error) {
	var (
		version domain.ConfigVersion
		id      string
		status  string
	)
	if err := row.Scan(
		&id, &version.Payload, &status, &version.CreatedAt,
		&version.ValidatedAt, &version.ValidationDetail, &version.ActivatedAt, &version.RejectedAt, &version.SupersededAt,
	); err != nil {
		return domain.ConfigVersion{}, err
	}
	version.ID = domain.ConfigVersionID(id)
	version.Status = domain.ConfigStatus(status)
	return version, nil
}
