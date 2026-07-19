package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// nodeStore is the PostgreSQL contracts.NodeStore. It owns SQL and row
// mapping and returns only domain.Node values across the boundary.
type nodeStore struct {
	q queryer
}

// NewNodeStore builds a pool-backed NodeStore for the direct (non-
// transactional) read path used by browse and query surfaces.
func NewNodeStore(pool *pgxpool.Pool) contracts.NodeStore {
	return &nodeStore{q: pool}
}

const nodeColumns = `id, work_id, parent_id, node_kind, media_type, container_type, item_type,
	title, natural_order, status, external_ids, attributes, created_at, updated_at`

func (s *nodeStore) Create(ctx context.Context, node domain.Node) (domain.Node, error) {
	// Canonicalise on write rather than trusting callers, so the open
	// vocabularies cannot fragment through a capability that spells a type
	// differently (ADR 0015).
	node = node.Canonical()
	_, err := s.q.Exec(ctx,
		`INSERT INTO nodes (id, work_id, parent_id, node_kind, media_type, container_type, item_type,
		                    title, natural_order, status, external_ids, attributes, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14)`,
		string(node.ID), string(node.WorkID), nodeIDParam(node.ParentID),
		string(node.Kind), string(node.MediaType),
		nullableText(string(node.ContainerType)), nullableText(string(node.ItemType)),
		node.Title, node.NaturalOrder, string(node.Status),
		jsonDocument(node.ExternalIDs), jsonDocument(node.Attributes),
		node.CreatedAt, node.UpdatedAt,
	)
	if err != nil {
		return domain.Node{}, mapError("create node", err)
	}
	return node, nil
}

func (s *nodeStore) FindByID(ctx context.Context, id domain.NodeID) (domain.Node, error) {
	row := s.q.QueryRow(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = $1`, string(id))
	node, err := scanNode(row)
	if err != nil {
		if isNoRows(err) {
			return domain.Node{}, contracts.NewError(contracts.NotFound, "node not found")
		}
		return domain.Node{}, mapError("find node by id", err)
	}
	return node, nil
}

func (s *nodeStore) Update(ctx context.Context, node domain.Node) (domain.Node, error) {
	node = node.Canonical()
	tag, err := s.q.Exec(ctx,
		`UPDATE nodes SET work_id = $2, parent_id = $3, node_kind = $4, media_type = $5,
		                  container_type = $6, item_type = $7, title = $8, natural_order = $9,
		                  status = $10, external_ids = $11, attributes = $12, updated_at = $13
		 WHERE id = $1`,
		string(node.ID), string(node.WorkID), nodeIDParam(node.ParentID),
		string(node.Kind), string(node.MediaType),
		nullableText(string(node.ContainerType)), nullableText(string(node.ItemType)),
		node.Title, node.NaturalOrder, string(node.Status),
		jsonDocument(node.ExternalIDs), jsonDocument(node.Attributes),
		node.UpdatedAt,
	)
	if err != nil {
		return domain.Node{}, mapError("update node", err)
	}
	if tag.RowsAffected() == 0 {
		return domain.Node{}, contracts.NewError(contracts.NotFound, "node not found")
	}
	return node, nil
}

// ListChildren is the load-bearing read: it rides nodes_parent_order_idx as a
// plain indexed scan, with no recursion at read time.
func (s *nodeStore) ListChildren(ctx context.Context, parentID domain.NodeID) ([]domain.Node, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes WHERE parent_id = $1 ORDER BY natural_order, id`,
		string(parentID),
	)
	if err != nil {
		return nil, mapError("list node children", err)
	}
	return collectNodes(rows, "list node children")
}

func (s *nodeStore) ListByWork(ctx context.Context, workID domain.NodeID) ([]domain.Node, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes WHERE work_id = $1 ORDER BY natural_order, id`,
		string(workID),
	)
	if err != nil {
		return nil, mapError("list nodes by work", err)
	}
	return collectNodes(rows, "list nodes by work")
}

// ListWorks reads the roots. The empty mediaType returns every root rather
// than none, so callers browsing the whole library need no second method.
func (s *nodeStore) ListWorks(ctx context.Context, mediaType domain.MediaType) ([]domain.Node, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes
		 WHERE parent_id IS NULL AND ($1 = '' OR media_type = $1)
		 ORDER BY title, id`,
		// Normalised on the way in as well as on the way out, or a caller
		// filtering by "Anime Series" would silently match nothing.
		string(domain.NormaliseMediaType(string(mediaType))),
	)
	if err != nil {
		return nil, mapError("list works", err)
	}
	return collectNodes(rows, "list works")
}

// Search applies the optional criteria of a NodeQuery.
//
// The title match is a substring, so it does not use the (media_type, title)
// index — it is a scan narrowed by whatever other criteria are present. That
// is adequate at the row counts a personal library reaches; a trigram index
// is the escalation if it stops being, and it is not needed to make the read
// correct.
func (s *nodeStore) Search(ctx context.Context, query contracts.NodeQuery) ([]domain.Node, error) {
	if query.Limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}

	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes
		 WHERE ($1 = '' OR title ILIKE $2 ESCAPE '\')
		   AND ($3 = '' OR media_type = $3)
		   AND ($4 = '' OR node_kind = $4)
		 ORDER BY title, id
		 LIMIT $5`,
		query.Title, likeContains(query.Title),
		string(domain.NormaliseMediaType(string(query.MediaType))),
		string(query.Kind),
		query.Limit,
	)
	if err != nil {
		return nil, mapError("search nodes", err)
	}
	return collectNodes(rows, "search nodes")
}

// FindByExternalID uses jsonb containment, which is what nodes_external_ids_gin
// indexes — the lookup this whole column exists to serve.
func (s *nodeStore) FindByExternalID(ctx context.Context, scheme, value string) ([]domain.Node, error) {
	if scheme == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "external id scheme is required")
	}
	if value == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "external id value is required")
	}

	// Built through the JSON encoder rather than by string concatenation, so
	// a scheme or value containing a quote is data and not syntax.
	document, err := json.Marshal(map[string]string{scheme: value})
	if err != nil {
		return nil, contracts.WrapError(contracts.InvalidArgument, "encode external id", err)
	}

	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes WHERE external_ids @> $1 ORDER BY title, id`,
		document,
	)
	if err != nil {
		return nil, mapError("find nodes by external id", err)
	}
	return collectNodes(rows, "find nodes by external id")
}

// likeContains renders a substring match, escaping the wildcards so a title
// containing % or _ is searched for literally rather than as a pattern.
func likeContains(s string) string {
	escaped := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
	return "%" + escaped + "%"
}

// Delete refuses rather than cascading. The parent_id and parts foreign keys
// are ON DELETE RESTRICT, so a node with children, parts or source bindings
// still behind it produces a foreign-key violation, which maps to Conflict —
// deletion is a decision a user confirms, never a silent cascade.
func (s *nodeStore) Delete(ctx context.Context, id domain.NodeID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, string(id))
	if err != nil {
		return mapError("delete node", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "node not found")
	}
	return nil
}

func scanNode(row pgx.Row) (domain.Node, error) {
	var (
		node          domain.Node
		id            string
		workID        string
		parentID      *string
		kind          string
		mediaType     string
		containerType *string
		itemType      *string
		status        string
	)
	if err := row.Scan(
		&id, &workID, &parentID, &kind, &mediaType, &containerType, &itemType,
		&node.Title, &node.NaturalOrder, &status,
		&node.ExternalIDs, &node.Attributes, &node.CreatedAt, &node.UpdatedAt,
	); err != nil {
		return domain.Node{}, err
	}
	node.ID = domain.NodeID(id)
	node.WorkID = domain.NodeID(workID)
	if parentID != nil {
		parent := domain.NodeID(*parentID)
		node.ParentID = &parent
	}
	node.Kind = domain.NodeKind(kind)
	node.MediaType = domain.MediaType(mediaType)
	if containerType != nil {
		node.ContainerType = domain.ContainerType(*containerType)
	}
	if itemType != nil {
		node.ItemType = domain.ItemType(*itemType)
	}
	node.Status = domain.NodeStatus(status)
	return node, nil
}

func collectNodes(rows pgx.Rows, message string) ([]domain.Node, error) {
	defer rows.Close()

	var nodes []domain.Node
	for rows.Next() {
		node, err := scanNode(rows)
		if err != nil {
			return nil, mapError("scan node row", err)
		}
		nodes = append(nodes, node)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError(message, err)
	}
	return nodes, nil
}

// nodeIDParam renders an optional parent as a nullable uuid parameter.
func nodeIDParam(id *domain.NodeID) any {
	if id == nil {
		return nil
	}
	return string(*id)
}

// nullableText maps the domain's "absent means empty string" convention onto
// the schema's nullable columns, so container_type and item_type are NULL
// rather than '' on the kinds they do not apply to.
func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// jsonDocument defaults an absent JSON document to an empty object. The
// columns are NOT NULL, and "no attributes yet" is an empty document rather
// than a missing one.
func jsonDocument(doc []byte) []byte {
	if len(doc) == 0 {
		return []byte(`{}`)
	}
	return doc
}
