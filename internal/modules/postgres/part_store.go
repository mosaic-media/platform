package postgres

import (
	"context"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// partStore is the PostgreSQL contracts.PartStore.
type partStore struct {
	q queryer
}

// NewPartStore builds a pool-backed PartStore for the direct (non-
// transactional) read path used during playback source selection.
func NewPartStore(pool *pgxpool.Pool) contracts.PartStore {
	return &partStore{q: pool}
}

const partColumns = `id, node_id, part_role, edition_label, natural_order,
	location_scheme, location_provider, location_ref,
	container, video_codec, audio_codec, width, height, hdr_format,
	duration_ns, bitrate_bps, size_bytes, attributes, created_at, updated_at`

func (s *partStore) Create(ctx context.Context, part v1.Part) (v1.Part, error) {
	_, err := s.q.Exec(ctx,
		`INSERT INTO parts (id, node_id, part_role, edition_label, natural_order,
		                    location_scheme, location_provider, location_ref,
		                    container, video_codec, audio_codec, width, height, hdr_format,
		                    duration_ns, bitrate_bps, size_bytes, attributes, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20)`,
		string(part.ID), string(part.NodeID), string(part.Role), part.EditionLabel, part.NaturalOrder,
		string(part.Location.Scheme), part.Location.Provider, part.Location.Ref,
		part.Container, part.VideoCodec, part.AudioCodec, part.Width, part.Height, part.HDRFormat,
		int64(part.Duration), part.BitrateBPS, part.SizeBytes,
		jsonDocument(part.Attributes), part.CreatedAt, part.UpdatedAt,
	)
	if err != nil {
		// The composite foreign key onto (nodes.id, nodes.node_kind) is what
		// keeps a Part from hanging off a work or container. It arrives as a
		// foreign-key violation, but attaching bytes to something unplayable is
		// a malformed request rather than a race, so it is InvalidArgument.
		if violatesConstraint(err, "parts_node_is_item") {
			return v1.Part{}, contracts.WrapError(contracts.InvalidArgument,
				"part must attach to an existing item node", err)
		}
		return v1.Part{}, mapError("create part", err)
	}
	return part, nil
}

func (s *partStore) FindByID(ctx context.Context, id v1.PartID) (v1.Part, error) {
	row := s.q.QueryRow(ctx, `SELECT `+partColumns+` FROM parts WHERE id = $1`, string(id))
	part, err := scanPart(row)
	if err != nil {
		if isNoRows(err) {
			return v1.Part{}, contracts.NewError(contracts.NotFound, "part not found")
		}
		return v1.Part{}, mapError("find part by id", err)
	}
	return part, nil
}

func (s *partStore) Update(ctx context.Context, part v1.Part) (v1.Part, error) {
	tag, err := s.q.Exec(ctx,
		`UPDATE parts SET node_id = $2, part_role = $3, edition_label = $4, natural_order = $5,
		                  location_scheme = $6, location_provider = $7, location_ref = $8,
		                  container = $9, video_codec = $10, audio_codec = $11,
		                  width = $12, height = $13, hdr_format = $14,
		                  duration_ns = $15, bitrate_bps = $16, size_bytes = $17,
		                  attributes = $18, updated_at = $19
		 WHERE id = $1`,
		string(part.ID), string(part.NodeID), string(part.Role), part.EditionLabel, part.NaturalOrder,
		string(part.Location.Scheme), part.Location.Provider, part.Location.Ref,
		part.Container, part.VideoCodec, part.AudioCodec, part.Width, part.Height, part.HDRFormat,
		int64(part.Duration), part.BitrateBPS, part.SizeBytes,
		jsonDocument(part.Attributes), part.UpdatedAt,
	)
	if err != nil {
		if violatesConstraint(err, "parts_node_is_item") {
			return v1.Part{}, contracts.WrapError(contracts.InvalidArgument,
				"part must attach to an existing item node", err)
		}
		return v1.Part{}, mapError("update part", err)
	}
	if tag.RowsAffected() == 0 {
		return v1.Part{}, contracts.NewError(contracts.NotFound, "part not found")
	}
	return part, nil
}

// ListByNode returns editions and segments together, in order. They share one
// list because they share one source-selection path.
func (s *partStore) ListByNode(ctx context.Context, nodeID v1.NodeID) ([]v1.Part, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+partColumns+` FROM parts WHERE node_id = $1 ORDER BY natural_order, id`,
		string(nodeID),
	)
	if err != nil {
		return nil, mapError("list parts by node", err)
	}
	defer rows.Close()

	var parts []v1.Part
	for rows.Next() {
		part, err := scanPart(rows)
		if err != nil {
			return nil, mapError("scan part row", err)
		}
		parts = append(parts, part)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("list parts by node", err)
	}
	return parts, nil
}

func (s *partStore) Delete(ctx context.Context, id v1.PartID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM parts WHERE id = $1`, string(id))
	if err != nil {
		return mapError("delete part", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "part not found")
	}
	return nil
}

func scanPart(row pgx.Row) (v1.Part, error) {
	var (
		part       v1.Part
		id         string
		nodeID     string
		role       string
		scheme     string
		durationNS int64
	)
	if err := row.Scan(
		&id, &nodeID, &role, &part.EditionLabel, &part.NaturalOrder,
		&scheme, &part.Location.Provider, &part.Location.Ref,
		&part.Container, &part.VideoCodec, &part.AudioCodec,
		&part.Width, &part.Height, &part.HDRFormat,
		&durationNS, &part.BitrateBPS, &part.SizeBytes,
		&part.Attributes, &part.CreatedAt, &part.UpdatedAt,
	); err != nil {
		return v1.Part{}, err
	}
	part.ID = v1.PartID(id)
	part.NodeID = v1.NodeID(nodeID)
	part.Role = v1.PartRole(role)
	part.Location.Scheme = v1.LocationScheme(scheme)
	part.Duration = time.Duration(durationNS)
	return part, nil
}
