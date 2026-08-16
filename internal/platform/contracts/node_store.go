// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package contracts

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// NodeStore persists the containment tree (platform#9).
//
// Every traversal here is by parent, never by an assumed level: variable
// depth is the property that lets a film be Work → Item and a series be
// Work → Container → Item without either being a special case, and it costs
// the discipline of never assuming a node has a parent or that a work's
// children are containers.
//
// Implementations must store the open type vocabularies canonically —
// v1.Node.Canonical() — so that "Anime Series", "anime-series" and
// "anime_series" are one media type rather than three (platform#11). Writes
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
	// before sourcing anything — and, with Offset, the read behind browsing
	// the library a page at a time.
	Search(ctx context.Context, query NodeQuery) ([]v1.Node, error)

	// Count is how many nodes Search would match, ignoring Limit and Offset.
	//
	// It exists so a browse surface over the library can state a real total,
	// which no other surface in Mosaic can: a provider's catalog is paged by
	// the provider and its size is unknown, so the catalog screen says "128+",
	// while the library is the install's own rows and can be counted. The
	// caller cannot get this by measuring the page it happens to hold.
	Count(ctx context.Context, query NodeQuery) (int, error)

	// Facets returns the distinct values a browse surface can offer as
	// narrowings, over the works the query already matches.
	//
	// It reads the library rather than a vocabulary. A facet built from a
	// fixed list offers chips that match nothing and omits the ones that
	// would, and this library's genres are several sources' words,
	// unreconciled, so no list written anywhere could be right. What a user is
	// offered is exactly what is on the shelf.
	//
	// A facet's own narrowing is ignored when computing that facet's set, so the
	// chips do not disappear as one is selected; every other criterion applies,
	// so the offered values are the ones that would actually return something.
	// The two facets narrow each other: selecting a genre reshapes the service
	// counts and the other way round, which is what makes two rows compose
	// rather than contradict.
	Facets(ctx context.Context, query NodeQuery) (Facets, error)

	// FindByExternalID looks nodes up by a provider's own identifier — the
	// strongest form of "do I already have this", and the one that does not
	// depend on titles matching.
	//
	// It reads the ExternalIDs document, which is a flat object of scheme to
	// identifier, so scheme "anilist" and value "1234" match a node carrying
	// {"anilist": "1234"}. More than one node may share an external id: an
	// anime and its source manga can carry the same provider reference, and
	// platform#9 keeps those as two Works rather than merging them.
	FindByExternalID(ctx context.Context, scheme, value string) ([]v1.Node, error)

	// Delete removes one node. It is Conflict when the node still has
	// children or parts: platform#9 rules that deletion is a decision a user
	// confirms, never a silent cascade, so the store refuses rather than
	// taking a subtree with it. Callers delete depth-first.
	Delete(ctx context.Context, id v1.NodeID) error
}

// Facets are the values a browse surface can narrow by, counted over what the
// rest of the query already matches.
//
// The service facet is not read from a module's attributes document: the
// Platform stores that uninterpreted (platform#9) and cannot enumerate the
// services in it without learning a module's key. It is built from what the
// Platform does model — see the availability index — rather than by reaching
// into somebody else's document.
type Facets struct {
	// Genres are the distinct genres present, ordered by how many works carry
	// them and then alphabetically, so a facet row leads with the ones worth
	// pressing. A genre no work carries does not appear.
	Genres []FacetValue
	// Services are the distinct streaming services present, on the same terms,
	// from the Platform's stored availability.
	//
	// The count is of works whose availability was fetched at all, which is
	// smaller than the library and says so on the screen. A service is offered
	// only when something is currently recorded on it, so a service every title
	// has left disappears from the row at the next refresh rather than standing
	// there returning nothing.
	Services []FacetValue
}

// FacetValue is one offerable narrowing and how many works it would leave.
//
// The count travels with the value because a chip that says how many titles are
// behind it is the difference between a control a user can aim and one they have
// to try. It is also what makes an empty result impossible to reach by pressing:
// nothing offered here can return nothing.
type FacetValue struct {
	Value string
	Count int
}

// NodeQuery narrows a content search. Every field is optional except Limit,
// and a zero-valued field matches everything, so the zero query with a limit
// is "the first N nodes".
//
// It is a struct where other stores take discrete arguments: their reads have
// one criterion each, while content search has several, and a method per
// combination would multiply without end.
type NodeQuery struct {
	// Title matches case-insensitively anywhere in the title. It is a
	// substring rather than a prefix because a user searching "alchemist"
	// expects to find "Fullmetal Alchemist".
	Title string
	// MediaType narrows to one media type, already normalised by the store.
	MediaType v1.MediaType
	// Kind narrows to works, containers or items. Searching for works alone
	// is the common case — a capability asks whether a show exists, not
	// whether some episode does.
	Kind v1.NodeKind
	// AttributesContain narrows to nodes whose attributes document contains
	// this JSON document. Empty means no filter.
	//
	// Containment rather than a typed filter because attributes are
	// module-owned and the Platform never interprets them (platform#9). It is
	// the same question FindByExternalID asks of the neighbouring document,
	// and it is answered by the same kind of index.
	//
	// A store must validate that the value is a JSON document. Passing
	// unparseable bytes through to the engine turns a caller's mistake into
	// a driver error, which crosses the boundary as Internal rather than as
	// the InvalidArgument it is.
	AttributesContain []byte
	// Genres narrows to nodes carrying every genre listed. Empty means no
	// filter.
	//
	// Conjunctive rather than disjunctive, because that is what a facet control
	// means when two chips are lit: "crime and comedy", not "either". One chip
	// reads the same under both rules, so the choice only shows itself when a
	// user has asked to narrow and the union would have widened instead.
	//
	// A store answers it against the stored strings, which are the source's own
	// words. The Platform reconciles no vocabularies here: "Sci-Fi" and "Science
	// Fiction" are two genres because two sources say so, and a synonym table
	// would be the Platform inventing a fact about somebody else's data.
	Genres []string
	// WatchProviders narrows to works recorded as available on every service
	// listed, in the stored region. Empty means no filter.
	//
	// It reads the Platform's own projection of ContentMetadata.Watch (see
	// WatchAvailabilityStore) and not any module's attributes document, so any
	// metadata provider that answers with availability populates it and no
	// module's key reaches the Platform. A work whose availability has never
	// been fetched does not match, which is correct: nothing is known about it,
	// and a facet that guessed would be the thing this whole slice avoids.
	WatchProviders []string
	// Limit caps the result set and must be positive. Search is a
	// user-facing read and an unbounded one is a denial of service against
	// a large library.
	Limit int
	// Offset skips the first N matches, for paging a browse surface. Zero is
	// the first page, and a negative value is the same as zero rather than an
	// error — a page number arriving from a client is arithmetic, and refusing
	// page -1 would turn a stale link into a broken screen.
	//
	// The order Search returns is total and stable (title, then id), which is
	// what makes offset paging correct here: an unordered offset would show and
	// hide rows at random as pages were turned.
	Offset int
}
