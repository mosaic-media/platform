// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Browsing what the install owns (roadmap M2.1).
//
// This is not SearchContent, although the two look alike. SearchContent answers
// "do I already have this?" for a capability about to source something: one
// question, one answer, a limit so it cannot be turned into a scan. Browsing is
// a page at a time, in a stable order, over a total the reader is told. Paging
// and a count belong to a Platform query rather than growing the surface every
// installed extension holds — the reasoning that kept watch history off
// ContentService (platform#59).

// defaultLibraryPageSize is one page of the library browse.
//
// A grid of posters, so the page is sized to a screenful and a bit rather than
// to a round number: too small and turning pages is the interaction, too large
// and the first render pays for rows nobody scrolled to.
const defaultLibraryPageSize = 60

// maxLibraryPageSize caps what a caller may ask for, as search's limit does. A
// browse is a user-facing read and an unbounded one is a denial of service
// against a large library.
//
// It is a window rather than a page, because the Library screen scrolls lazily:
// each further page re-renders the screen holding everything loaded so far (a
// query action replaces the content region, it does not append), so the read
// grows with the scroll. This is where that growth stops, and the screen says so
// rather than silently ending the scroll.
const maxLibraryPageSize = 600

// ListLibraryQuery is one page of the materialised library.
type ListLibraryQuery struct {
	Caller v1.Caller
	// MediaType optionally narrows to one media type, in the Platform's
	// canonical vocabulary (platform#11). Empty is everything.
	MediaType v1.MediaType
	// Title optionally narrows by title substring. It is here because the store
	// read carries it and a browse over a large library wants it, not because
	// any surface passes it yet.
	Title string
	// Genres narrows to works carrying every genre listed — the library facet
	// (roadmap M2.4). Empty is everything.
	Genres []string
	// WatchProviders narrows to works available on every service listed. Empty
	// is everything.
	//
	// It must read the Platform's own projection of what a metadata provider
	// said, kept current by the availability refresh, and not the module-owned
	// attributes document module-tmdb writes at import. Both hold the same kind
	// of fact; only one is refreshed, and only one is the Platform's to
	// interpret.
	//
	// No screen passes this. The Library screen deliberately offers no
	// streaming-service facet: availability answers "what could I watch on this
	// service", which spans what the library holds and what it does not, and
	// answering it over the shelf alone hands a user the small half while looking
	// like the whole. This filter is what a union of the two would read the
	// library half from without a provider round trip per title; until something
	// does, it is a capability with no client path — see the
	// unreachable-capability register.
	WatchProviders []string
	// Limit and Offset page the result. Zero Limit takes the default.
	Limit  int
	Offset int
}

// ListLibraryResult is one page, and the truth about how big the whole thing is.
type ListLibraryResult struct {
	// Works are the roots of this page's trees, ordered by title.
	Works []v1.Node
	// Total is how many works match the query in full — not how many are on
	// this page.
	//
	// A real number, not a lower bound. The catalog screen renders "128+"
	// because a provider pages its own catalog and will not say how large it is;
	// these are the install's own rows and can be counted.
	Total int
	// Offset and Limit are echoed back so a caller rendering paging controls does
	// not have to re-derive the page it asked for, and so a clamped or defaulted
	// value is visible rather than applied silently.
	Offset int
	Limit  int
	// Facets are the narrowings this browse can offer, over what the rest of
	// the query already matches — see contracts.Facets. They are read from the
	// library rather than from a vocabulary, so a chip that is offered always
	// returns something and a genre nothing carries is never drawn.
	Facets contracts.Facets
}

// HasMore reports whether another page follows this one.
func (r ListLibraryResult) HasMore() bool { return r.Offset+len(r.Works) < r.Total }

// ListLibrary reads one page of the materialised library, with the total the
// page is drawn from.
//
// It authorises content.read, the same action every other library read takes:
// there is one shared library and everybody who may see the library may see all
// of it (platform#59). What differs per person is what they did with it, which is
// a different query.
func (s *Service) ListLibrary(ctx context.Context, q ListLibraryQuery) (ListLibraryResult, error) {
	if q.Caller.Session == "" {
		return ListLibraryResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return ListLibraryResult{}, err
	}

	limit := q.Limit
	switch {
	case limit <= 0:
		limit = defaultLibraryPageSize
	case limit > maxLibraryPageSize:
		limit = maxLibraryPageSize
	}
	offset := q.Offset
	if offset < 0 {
		// A page number is arithmetic on a client's side, and a stale link is an
		// ordinary thing to follow. Page -1 is the first page, not an error.
		offset = 0
	}

	// Works only: a household owns a series rather than sixty-four episodes of
	// it, and the tree below a work is what the detail screen is for.
	query := contracts.NodeQuery{
		Title:          q.Title,
		MediaType:      q.MediaType,
		Kind:           v1.NodeWork,
		Genres:         q.Genres,
		WatchProviders: q.WatchProviders,
		Limit:          limit,
		Offset:         offset,
	}

	// The count first, so a page beyond the end is reported against a real total
	// rather than as an empty library. Two reads rather than one window function,
	// so the store can index each the way it wants to.
	total, err := s.nodes.Count(ctx, query)
	if err != nil {
		return ListLibraryResult{}, err
	}
	works, err := s.nodes.Search(ctx, query)
	if err != nil {
		return ListLibraryResult{}, err
	}
	facets, err := s.nodes.Facets(ctx, query)
	if err != nil {
		return ListLibraryResult{}, err
	}

	return ListLibraryResult{
		Works: works, Total: total, Offset: offset, Limit: limit, Facets: facets,
	}, nil
}
