// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// nodeStore is the PostgreSQL contracts.NodeStore. It owns SQL and row
// mapping and returns only v1.Node values across the boundary.
type nodeStore struct {
	q queryer
}

// NewNodeStore builds a pool-backed NodeStore for the direct (non-
// transactional) read path used by browse and query surfaces.
func NewNodeStore(pool *pgxpool.Pool) contracts.NodeStore {
	return &nodeStore{q: pool}
}

const nodeColumns = `id, work_id, parent_id, node_kind, media_type, container_type, item_type,
	title, natural_order, status, external_ids, attributes, artwork, genres, created_at, updated_at`

func (s *nodeStore) Create(ctx context.Context, node v1.Node) (v1.Node, error) {
	// Canonicalise on write rather than trusting callers, so the open
	// vocabularies cannot fragment through a capability that spells a type
	// differently (platform#11).
	node = node.Canonical()
	_, err := s.q.Exec(ctx,
		`INSERT INTO nodes (id, work_id, parent_id, node_kind, media_type, container_type, item_type,
		                    title, natural_order, status, external_ids, attributes, artwork, genres, created_at, updated_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16)`,
		string(node.ID), string(node.WorkID), nodeIDParam(node.ParentID),
		string(node.Kind), string(node.MediaType),
		nullableText(string(node.ContainerType)), nullableText(string(node.ItemType)),
		node.Title, node.NaturalOrder, string(node.Status),
		jsonDocument(node.ExternalIDs), jsonDocument(node.Attributes), artworkDocument(node.Artwork),
		genreArray(node.Genres),
		node.CreatedAt, node.UpdatedAt,
	)
	if err != nil {
		return v1.Node{}, mapError("create node", err)
	}
	return node, nil
}

func (s *nodeStore) FindByID(ctx context.Context, id v1.NodeID) (v1.Node, error) {
	row := s.q.QueryRow(ctx, `SELECT `+nodeColumns+` FROM nodes WHERE id = $1`, string(id))
	node, err := scanNode(row)
	if err != nil {
		if isNoRows(err) {
			return v1.Node{}, contracts.NewError(contracts.NotFound, "node not found")
		}
		return v1.Node{}, mapError("find node by id", err)
	}
	return node, nil
}

func (s *nodeStore) Update(ctx context.Context, node v1.Node) (v1.Node, error) {
	node = node.Canonical()
	tag, err := s.q.Exec(ctx,
		`UPDATE nodes SET work_id = $2, parent_id = $3, node_kind = $4, media_type = $5,
		                  container_type = $6, item_type = $7, title = $8, natural_order = $9,
		                  status = $10, external_ids = $11, attributes = $12, artwork = $13,
		                  genres = $14, updated_at = $15
		 WHERE id = $1`,
		string(node.ID), string(node.WorkID), nodeIDParam(node.ParentID),
		string(node.Kind), string(node.MediaType),
		nullableText(string(node.ContainerType)), nullableText(string(node.ItemType)),
		node.Title, node.NaturalOrder, string(node.Status),
		jsonDocument(node.ExternalIDs), jsonDocument(node.Attributes), artworkDocument(node.Artwork),
		genreArray(node.Genres),
		node.UpdatedAt,
	)
	if err != nil {
		return v1.Node{}, mapError("update node", err)
	}
	if tag.RowsAffected() == 0 {
		return v1.Node{}, contracts.NewError(contracts.NotFound, "node not found")
	}
	return node, nil
}

// ListChildren is the load-bearing read: it rides nodes_parent_order_idx as a
// plain indexed scan, with no recursion at read time.
func (s *nodeStore) ListChildren(ctx context.Context, parentID v1.NodeID) ([]v1.Node, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes WHERE parent_id = $1 ORDER BY natural_order, id`,
		string(parentID),
	)
	if err != nil {
		return nil, mapError("list node children", err)
	}
	return collectNodes(rows, "list node children")
}

func (s *nodeStore) ListByWork(ctx context.Context, workID v1.NodeID) ([]v1.Node, error) {
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
func (s *nodeStore) ListWorks(ctx context.Context, mediaType v1.MediaType) ([]v1.Node, error) {
	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes
		 WHERE parent_id IS NULL AND ($1 = '' OR media_type = $1)
		 ORDER BY title, id`,
		// Normalised on the way in as well as on the way out, or a caller
		// filtering by "Anime Series" would silently match nothing.
		string(v1.NormaliseMediaType(string(mediaType))),
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
func (s *nodeStore) Search(ctx context.Context, query contracts.NodeQuery) ([]v1.Node, error) {
	if query.Limit <= 0 {
		return nil, contracts.NewError(contracts.InvalidArgument, "limit must be positive")
	}

	// The attribute filter is the one criterion that can be *malformed* rather
	// than merely empty, so it is rejected here instead of being handed to the
	// driver — a cast failure inside the engine would surface as Internal, which
	// tells the caller nothing about the mistake being theirs.
	var attributes any
	if len(query.AttributesContain) > 0 {
		if !json.Valid(query.AttributesContain) {
			return nil, contracts.NewError(contracts.InvalidArgument, "attributes filter must be a valid JSON document")
		}
		attributes = query.AttributesContain
	}
	genres := genreFilter(query.Genres)
	services := genreFilter(query.WatchProviders)

	offset := query.Offset
	if offset < 0 {
		offset = 0
	}

	// `@>` over the nodes_attributes_gin index, exactly as FindByExternalID
	// reads the neighbouring external_ids document. The NULL check is what makes
	// the filter optional without building the SQL by concatenation.
	//
	// ORDER BY title, id is a *total* order and that is what makes OFFSET
	// paging correct: title alone ties for a household holding two cuts of one
	// film, and a tie under LIMIT/OFFSET is how a row appears on two pages
	// while another appears on none.
	rows, err := s.q.Query(ctx,
		`SELECT `+nodeColumns+` FROM nodes
		 WHERE ($1 = '' OR title ILIKE $2 ESCAPE '\')
		   AND ($3 = '' OR media_type = $3)
		   AND ($4 = '' OR node_kind = $4)
		   AND ($5::jsonb IS NULL OR attributes @> $5::jsonb)
		   AND ($6::text[] IS NULL OR genres @> $6::text[])
		   AND ($7::text[] IS NULL OR EXISTS (
		         SELECT 1 FROM node_watch_availability w
		         WHERE w.node_id = nodes.id AND w.providers @> $7::text[]))
		 ORDER BY title, id
		 LIMIT $8 OFFSET $9`,
		query.Title, likeContains(query.Title),
		string(v1.NormaliseMediaType(string(query.MediaType))),
		string(query.Kind),
		attributes, genres, services,
		query.Limit, offset,
	)
	if err != nil {
		return nil, mapError("search nodes", err)
	}
	return collectNodes(rows, "search nodes")
}

// Count answers how many rows Search would match, with the same predicate and
// without Limit or Offset.
//
// The predicate is repeated rather than shared with Search through a
// concatenated fragment, because the two statements are read together and a
// helper that built the WHERE clause for both would hide the one property that
// matters — that they filter identically. A count that disagrees with the page
// beneath it is worse than no count at all.
//
// It does not validate Limit: counting is not paging, so a query with no limit
// is a legitimate thing to count.
func (s *nodeStore) Count(ctx context.Context, query contracts.NodeQuery) (int, error) {
	var attributes any
	if len(query.AttributesContain) > 0 {
		if !json.Valid(query.AttributesContain) {
			return 0, contracts.NewError(contracts.InvalidArgument, "attributes filter must be a valid JSON document")
		}
		attributes = query.AttributesContain
	}
	genres := genreFilter(query.Genres)
	services := genreFilter(query.WatchProviders)

	row := s.q.QueryRow(ctx,
		`SELECT count(*) FROM nodes
		 WHERE ($1 = '' OR title ILIKE $2 ESCAPE '\')
		   AND ($3 = '' OR media_type = $3)
		   AND ($4 = '' OR node_kind = $4)
		   AND ($5::jsonb IS NULL OR attributes @> $5::jsonb)
		   AND ($6::text[] IS NULL OR genres @> $6::text[])
		   AND ($7::text[] IS NULL OR EXISTS (
		         SELECT 1 FROM node_watch_availability w
		         WHERE w.node_id = nodes.id AND w.providers @> $7::text[]))`,
		query.Title, likeContains(query.Title),
		string(v1.NormaliseMediaType(string(query.MediaType))),
		string(query.Kind),
		attributes, genres, services,
	)
	var total int
	if err := row.Scan(&total); err != nil {
		return 0, mapError("count nodes", err)
	}
	return total, nil
}

// Facets enumerates the narrowings present in what the rest of the query
// matches — the genres carried on the nodes, and the services in the Platform's
// stored availability.
//
// `unnest` rather than a normalised table on either side: both sets are small
// (tens of values over hundreds of works), both are read once per screen render,
// and a lookup table would be a join and a write path for values nothing updates
// independently of the rows they hang off.
//
// **A facet's own narrowing is not applied to that facet**, so selecting a chip
// does not empty the row that offered it — but the *other* facet's is, so the
// two rows compose: picking a service reshapes the genre counts and the other
// way round. Every remaining criterion applies to both, which is what makes an
// empty result unreachable by pressing.
func (s *nodeStore) Facets(ctx context.Context, query contracts.NodeQuery) (contracts.Facets, error) {
	var attributes any
	if len(query.AttributesContain) > 0 {
		if !json.Valid(query.AttributesContain) {
			return contracts.Facets{}, contracts.NewError(contracts.InvalidArgument, "attributes filter must be a valid JSON document")
		}
		attributes = query.AttributesContain
	}
	genres := genreFilter(query.Genres)
	services := genreFilter(query.WatchProviders)

	var out contracts.Facets

	// The genre set honours the *service* narrowing and ignores its own, so the
	// two rows compose: picking Netflix reshapes the genre counts, and picking a
	// genre does not empty the row that offered it.
	genreRows, err := s.q.Query(ctx,
		`SELECT genre, count(*) FROM (
		   SELECT unnest(genres) AS genre FROM nodes
		   WHERE ($1 = '' OR title ILIKE $2 ESCAPE '\')
		     AND ($3 = '' OR media_type = $3)
		     AND ($4 = '' OR node_kind = $4)
		     AND ($5::jsonb IS NULL OR attributes @> $5::jsonb)
		     AND ($6::text[] IS NULL OR EXISTS (
		           SELECT 1 FROM node_watch_availability w
		           WHERE w.node_id = nodes.id AND w.providers @> $6::text[]))
		 ) g
		 WHERE genre <> ''
		 GROUP BY genre
		 ORDER BY count(*) DESC, genre`,
		query.Title, likeContains(query.Title),
		string(v1.NormaliseMediaType(string(query.MediaType))),
		string(query.Kind),
		attributes, services,
	)
	if err != nil {
		return contracts.Facets{}, mapError("read node facets", err)
	}
	out.Genres, err = collectFacetValues(genreRows)
	if err != nil {
		return contracts.Facets{}, err
	}

	// And the service set the other way round.
	serviceRows, err := s.q.Query(ctx,
		`SELECT service, count(*) FROM (
		   SELECT unnest(w.providers) AS service
		   FROM nodes JOIN node_watch_availability w ON w.node_id = nodes.id
		   WHERE ($1 = '' OR nodes.title ILIKE $2 ESCAPE '\')
		     AND ($3 = '' OR nodes.media_type = $3)
		     AND ($4 = '' OR nodes.node_kind = $4)
		     AND ($5::jsonb IS NULL OR nodes.attributes @> $5::jsonb)
		     AND ($6::text[] IS NULL OR nodes.genres @> $6::text[])
		 ) g
		 WHERE service <> ''
		 GROUP BY service
		 ORDER BY count(*) DESC, service`,
		query.Title, likeContains(query.Title),
		string(v1.NormaliseMediaType(string(query.MediaType))),
		string(query.Kind),
		attributes, genres,
	)
	if err != nil {
		return contracts.Facets{}, mapError("read node facets", err)
	}
	out.Services, err = collectFacetValues(serviceRows)
	if err != nil {
		return contracts.Facets{}, err
	}
	return out, nil
}

// collectFacetValues drains one facet query into its values.
func collectFacetValues(rows pgx.Rows) ([]contracts.FacetValue, error) {
	defer rows.Close()
	var out []contracts.FacetValue
	for rows.Next() {
		var value contracts.FacetValue
		if err := rows.Scan(&value.Value, &value.Count); err != nil {
			return nil, mapError("read node facets", err)
		}
		out = append(out, value)
	}
	if err := rows.Err(); err != nil {
		return nil, mapError("read node facets", err)
	}
	return out, nil
}

// FindByExternalID uses jsonb containment, which is what nodes_external_ids_gin
// indexes — the lookup this whole column exists to serve.
func (s *nodeStore) FindByExternalID(ctx context.Context, scheme, value string) ([]v1.Node, error) {
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
func (s *nodeStore) Delete(ctx context.Context, id v1.NodeID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM nodes WHERE id = $1`, string(id))
	if err != nil {
		return mapError("delete node", err)
	}
	if tag.RowsAffected() == 0 {
		return contracts.NewError(contracts.NotFound, "node not found")
	}
	return nil
}

func scanNode(row pgx.Row) (v1.Node, error) {
	var (
		node          v1.Node
		id            string
		workID        string
		parentID      *string
		kind          string
		mediaType     string
		containerType *string
		itemType      *string
		status        string
		artwork       []byte
	)
	if err := row.Scan(
		&id, &workID, &parentID, &kind, &mediaType, &containerType, &itemType,
		&node.Title, &node.NaturalOrder, &status,
		&node.ExternalIDs, &node.Attributes, &artwork, &node.Genres, &node.CreatedAt, &node.UpdatedAt,
	); err != nil {
		return v1.Node{}, err
	}
	// artwork is NOT NULL DEFAULT '{}', so this is at worst an empty object that
	// unmarshals to the zero Artwork — never a nil to guard.
	if err := json.Unmarshal(artwork, &node.Artwork); err != nil {
		return v1.Node{}, err
	}
	node.ID = v1.NodeID(id)
	node.WorkID = v1.NodeID(workID)
	if parentID != nil {
		parent := v1.NodeID(*parentID)
		node.ParentID = &parent
	}
	node.Kind = v1.NodeKind(kind)
	node.MediaType = v1.MediaType(mediaType)
	if containerType != nil {
		node.ContainerType = v1.ContainerType(*containerType)
	}
	if itemType != nil {
		node.ItemType = v1.ItemType(*itemType)
	}
	node.Status = v1.NodeStatus(status)
	return node, nil
}

func collectNodes(rows pgx.Rows, message string) ([]v1.Node, error) {
	defer rows.Close()

	var nodes []v1.Node
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
func nodeIDParam(id *v1.NodeID) any {
	if id == nil {
		return nil
	}
	return string(*id)
}

// nullableText maps the domain's "absent means empty string" convention onto
// the schema's nullable columns, so container_type and item_type are NULL
// rather than ” on the kinds they do not apply to.
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

// genreArray renders a node's genres as its text[] column value. The column is
// NOT NULL, so nil becomes the empty array rather than a NULL — "no genres are
// known" is one fact and it has one representation.
func genreArray(genres []string) []string {
	if genres == nil {
		return []string{}
	}
	return genres
}

// genreFilter renders a query's genre narrowing as a nullable text[] parameter.
//
// nil rather than an empty array when nothing is selected, because the
// predicate is `$n IS NULL OR genres @> $n` and `genres @> '{}'` is *true for
// every row*: an empty array would read as "no filter" by accident rather than
// by intent, and would go on reading that way if the predicate were ever
// tightened.
//
// `@>` and not `&&`: containment is the conjunctive reading — a row must carry
// every genre asked for — which is what two lit chips on a facet control mean.
// `&&` is the union, and it would widen a selection a user made to narrow.
func genreFilter(genres []string) any {
	if len(genres) == 0 {
		return nil
	}
	return genres
}

// artworkDocument renders stored artwork as its jsonb column value. Artwork is a
// struct of strings, so marshalling it never fails; an empty value becomes `{}`
// through the omitempty tags, matching the column default.
func artworkDocument(a v1.Artwork) []byte {
	doc, err := json.Marshal(a)
	if err != nil {
		return []byte(`{}`)
	}
	return doc
}
