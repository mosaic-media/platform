package contracts

import (
	"context"

	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// NodeStore persists the containment tree (ADR 0013).
//
// Every traversal here is by parent, never by an assumed level: variable
// depth is the property that lets a film be Work → Item and a series be
// Work → Container → Item without either being a special case, and it costs
// the discipline of never assuming a node has a parent or that a work's
// children are containers.
//
// Implementations must store the open type vocabularies canonically —
// v1.Node.Canonical() — so that "Anime Series", "anime-series" and
// "anime_series" are one media type rather than three (ADR 0015). Writes
// return the canonical value, which may therefore differ from what was
// passed in.
type NodeStore interface {
	Create(ctx context.Context, node v1.Node) (v1.Node, error)
	FindByID(ctx context.Context, id v1.NodeID) (v1.Node, error)
	Update(ctx context.Context, node v1.Node) (v1.Node, error)

	// ListChildren returns the direct children of a node ordered by
	// NaturalOrder. This is the single most common query a media browser
	// makes and it is served by a plain indexed scan — no recursion at read
	// time.
	ListChildren(ctx context.Context, parentID v1.NodeID) ([]v1.Node, error)

	// ListByWork returns every node in one work's tree, the work itself
	// included, ordered by NaturalOrder. It reads the denormalised work id
	// rather than walking parents.
	ListByWork(ctx context.Context, workID v1.NodeID) ([]v1.Node, error)

	// ListWorks returns the root of every tree — the nodes with no parent —
	// optionally narrowed to one media type. An empty mediaType returns all
	// of them.
	ListWorks(ctx context.Context, mediaType v1.MediaType) ([]v1.Node, error)

	// Search finds nodes matching a set of optional criteria. It is the read
	// behind "do I already have this?" — the question a capability asks
	// before sourcing anything.
	Search(ctx context.Context, query NodeQuery) ([]v1.Node, error)

	// FindByExternalID looks nodes up by a provider's own identifier — the
	// strongest form of "do I already have this", and the one that does not
	// depend on titles matching.
	//
	// It reads the ExternalIDs document, which is a flat object of scheme to
	// identifier, so scheme "anilist" and value "1234" match a node carrying
	// {"anilist": "1234"}. More than one node may share an external id: an
	// anime and its source manga can carry the same provider reference, and
	// ADR 0013 keeps those as two Works rather than merging them.
	FindByExternalID(ctx context.Context, scheme, value string) ([]v1.Node, error)

	// Delete removes one node. It is Conflict when the node still has
	// children or parts: ADR 0013 rules that deletion is a decision a user
	// confirms, never a silent cascade, so the store refuses rather than
	// taking a subtree with it. Callers delete depth-first.
	Delete(ctx context.Context, id v1.NodeID) error
}

// NodeQuery narrows a content search. Every field is optional except Limit,
// and a zero-valued field matches everything, so the zero query with a limit
// is "the first N nodes".
//
// This is the first filter struct in the contract set. The stores elsewhere
// take discrete arguments because their reads have one criterion each;
// content search genuinely has several, and a method per combination would
// multiply without end.
type NodeQuery struct {
	// Title matches case-insensitively anywhere in the title. It is a
	// substring rather than a prefix because a user searching "alchemist"
	// expects to find "Fullmetal Alchemist".
	Title string
	// MediaType narrows to one media type, already normalised by the store.
	MediaType v1.MediaType
	// Kind narrows to works, containers or items. Searching for works alone
	// is the common case — a capability asks whether a *show* exists, not
	// whether some episode does.
	Kind v1.NodeKind
	// Limit caps the result set and must be positive. Search is a
	// user-facing read and an unbounded one is a denial of service against
	// a large library.
	Limit int
}
