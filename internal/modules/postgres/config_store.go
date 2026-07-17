package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// configStore is the PostgreSQL contracts.ConfigStore. Persistence only:
// activation and validation flows are a later slice (MEG-015 §08).
type configStore struct {
	q queryer
}

// NewConfigStore builds a pool-backed ConfigStore.
func NewConfigStore(pool *pgxpool.Pool) contracts.ConfigStore {
	return &configStore{q: pool}
}

const configColumns = `id, payload, created_at, activated_at`

func (s *configStore) Save(ctx context.Context, version domain.ConfigVersion) (domain.ConfigVersion, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO config_versions (id, payload, created_at, activated_at) VALUES ($1, $2, $3, $4)`,
		string(version.ID), version.Payload, version.CreatedAt, version.ActivatedAt,
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

func scanConfigVersion(row pgx.Row) (domain.ConfigVersion, error) {
	var (
		version     domain.ConfigVersion
		id          string
		activatedAt *time.Time
	)
	if err := row.Scan(&id, &version.Payload, &version.CreatedAt, &activatedAt); err != nil {
		return domain.ConfigVersion{}, err
	}
	version.ID = domain.ConfigVersionID(id)
	version.ActivatedAt = activatedAt
	return version, nil
}
