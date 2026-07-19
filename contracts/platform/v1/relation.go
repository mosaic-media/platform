package v1

import "time"

// RelationType is the kind of association an edge asserts (ADR 0013).
// Unlike MediaType this is closed and Platform-owned: it is the vocabulary
// of the association graph itself, not of the things being associated, and
// code reads specific types to build specific features.
type RelationType string

const (
	// RelationAdaptation joins a work to the work it adapts. An anime and
	// its source manga are two separate Works joined by this edge — they
	// have different part structures and frequently diverge in canon, so
	// forcing them into one tree would corrupt both.
	RelationAdaptation RelationType = "adaptation"
	// RelationSequel joins a work to the work that follows it.
	RelationSequel RelationType = "sequel"
	// RelationPrequel joins a work to the work that precedes it.
	RelationPrequel RelationType = "prequel"
	// RelationSpinoff joins a work to a work derived from it.
	RelationSpinoff RelationType = "spinoff"
	// RelationCollectionMember joins a collection Work to a member Work.
	// A collection is not a second concept — it is a Node with
	// MediaCollection and no items of its own, joined to its members by
	// these edges. A collected edition of a comic run is its own Work
	// related to what it collects by exactly this mechanism.
	RelationCollectionMember RelationType = "collection_member"
	// RelationAlternateEditionOf joins two works that are renderings of the
	// same underlying thing but are not one Item's editions.
	RelationAlternateEditionOf RelationType = "alternate_edition_of"
	// RelationSameFranchise joins works sharing a franchise without a
	// stronger relationship.
	RelationSameFranchise RelationType = "same_franchise"
)

// RelationOrigin records where an edge came from, which is what makes a
// low confidence score actionable.
type RelationOrigin string

const (
	// OriginSystemInferred is an edge a background job computed.
	OriginSystemInferred RelationOrigin = "system_inferred"
	// OriginProviderSupplied is an edge a metadata provider asserted.
	OriginProviderSupplied RelationOrigin = "provider_supplied"
	// OriginUserConfirmed is an edge a user affirmed.
	OriginUserConfirmed RelationOrigin = "user_confirmed"
)

// Relation is a typed, directed, confidence-scored edge in the association
// graph (ADR 0013).
//
// Containment and association are separate structures on purpose.
// Containment — a season contains episodes — is a tree and lives in Node.
// Association — this anime adapts that manga, these films belong to one
// collection — is a graph, and it does not nest. This is the graph.
//
// Edges are written once and never age: ADR 0013 records that relation
// confidence has no decay or reverification policy, so Confidence is the
// score at write time and nothing rechecks it. There is deliberately no
// UpdatedAt.
type Relation struct {
	ID         RelationID
	FromNodeID NodeID
	ToNodeID   NodeID
	Type       RelationType
	// Confidence is between 0 and 1 inclusive, enforced by the database.
	Confidence float64
	Origin     RelationOrigin
	CreatedAt  time.Time
}
