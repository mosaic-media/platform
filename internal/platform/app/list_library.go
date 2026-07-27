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
// Nothing in Mosaic did this. Home renders provider catalogs, `collections` and
// `catalog` browse a *module's* collections, and search unions the library with
// the providers — so every existing browse surface is a window onto somebody
// else's shelf. `SearchContent`, the library-only read on the published surface,
// had no client path at all.
//
// This is deliberately **not** SearchContent, and the reason is worth stating
// because the two look alike. SearchContent answers "do I already have this?"
// for a capability about to source something: one question, one answer, a limit
// so it cannot be turned into a scan. Browsing is the other shape — a page at a
// time, in a stable order, over a total the reader is told. Paging and a count
// are what a browse surface needs and what nothing sourcing content has ever
// asked for, so they belong to a Platform query rather than growing the surface
// every installed extension holds. It is the same reasoning that kept watch
// history off `ContentService` (ADR 0103).

// defaultLibraryPageSize is one page of the library browse.
//
// A grid of posters, so the page is sized to a screenful and a bit rather than
// to a round number: too small and turning pages is the interaction, too large
// and the first render pays for rows nobody scrolled to.
const defaultLibraryPageSize = 60

// maxLibraryPageSize caps what a caller may ask for, as search's limit does. A
// browse is a user-facing read and an unbounded one is a denial of service
// against a large library.
const maxLibraryPageSize = 200

// ListLibraryQuery is one page of the materialised library.
type ListLibraryQuery struct {
	Caller v1.Caller
	// MediaType optionally narrows to one media type, in the Platform's
	// canonical vocabulary (ADR 0015). Empty is everything.
	MediaType v1.MediaType
	// Title optionally narrows by title substring. It is here because the store
	// read carries it and a browse over a large library wants it, not because
	// any surface passes it yet.
	Title string
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
	// **A real number, which is the point.** The catalog screen renders "128+"
	// because a provider pages its own catalog and will not say how large it
	// is; these are the install's own rows, and a browse over them that said
	// "60+" would be understating something it can count. That difference is
	// the whole reason this result carries a total at all.
	Total int
	// Offset and Limit are echoed back so a caller rendering paging controls
	// does not have to re-derive the page it asked for — and so that a clamped
	// or defaulted value is visible to the caller rather than being applied
	// silently.
	Offset int
	Limit  int
}

// HasMore reports whether another page follows this one.
func (r ListLibraryResult) HasMore() bool { return r.Offset+len(r.Works) < r.Total }

// ListLibrary reads one page of the materialised library, with the total the
// page is drawn from.
//
// It authorises `content.read`, the same action every other library read takes:
// there is one shared library and everybody who may see the library may see all
// of it (ADR 0103). What differs per person is what they *did* with it, which is
// a different query.
func (s *Service) ListLibrary(ctx context.Context, q ListLibraryQuery) (ListLibraryResult, error) {
	// 1. validate query shape.
	if q.Caller.Session == "" {
		return ListLibraryResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	// 2-3. authenticate the caller and authorize the action.
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

	// 4. load state through a read contract. Works only: the library browse
	// shows what a household owns, and a household owns *Fullmetal Alchemist*
	// rather than sixty-four episodes of it. The tree below a work is what the
	// detail screen is for.
	query := contracts.NodeQuery{
		Title:     q.Title,
		MediaType: q.MediaType,
		Kind:      v1.NodeWork,
		Limit:     limit,
		Offset:    offset,
	}

	// The count first, so that a page beyond the end can still be reported
	// against a real total rather than as an empty library. Two reads rather
	// than one window function, because the count is the same predicate without
	// the paging and keeping them as two statements is what lets the store index
	// each the way it wants to.
	total, err := s.nodes.Count(ctx, query)
	if err != nil {
		return ListLibraryResult{}, err
	}
	works, err := s.nodes.Search(ctx, query)
	if err != nil {
		return ListLibraryResult{}, err
	}

	return ListLibraryResult{Works: works, Total: total, Offset: offset, Limit: limit}, nil
}
