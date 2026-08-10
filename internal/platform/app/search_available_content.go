// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// SearchAvailableContentQuery is a free-text search over what the enabled
// modules can source — the discovery surface that lets a user search Mosaic
// without a raw provider id (platform#18). It is a Platform query, not part of the
// published ContentService: it drives the modules, they do not call it.
type SearchAvailableContentQuery struct {
	Caller    v1.Caller
	Text      string
	MediaType v1.MediaType
	Limit     int
}

// SearchAvailableContentResult carries the union of provider candidates, each
// marked whether it is already in the library.
type SearchAvailableContentResult struct {
	Results []v1.SearchResult
}

// SearchAvailableContent fans the query out to every registered SearchProvider,
// unions the virtual candidates, and marks each one in-library or not (ADR
// 0028's union). A provider that errors is skipped rather than failing the whole
// search — a source being down empties its plane, it does not blank the others.
// Nothing here writes: the results are virtual until a caller materialises one.
func (s *Service) SearchAvailableContent(ctx context.Context, q SearchAvailableContentQuery) (SearchAvailableContentResult, error) {
	if q.Caller.Session == "" {
		return SearchAvailableContentResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if strings.TrimSpace(q.Text) == "" {
		return SearchAvailableContentResult{}, contracts.NewError(contracts.InvalidArgument, "search text is required")
	}

	az, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"})
	if err != nil {
		return SearchAvailableContentResult{}, err
	}

	if s.capabilities == nil {
		return SearchAvailableContentResult{}, nil
	}

	// Fan the query out to the providers concurrently; each is an independent
	// remote round-trip. fanOut preserves provider order and the two error paths:
	// a settings read that fails aborts the query, a provider that is down is
	// skipped (nil, nil) so its plane empties without blanking the others.
	//
	// The fallback tier answers only when nothing else found the title. The
	// Platform still does no cross-provider dedup (module-cinemeta#1 records this), so
	// two general metadata sources answering one query is the same title twice —
	// and unlike a catalog row, a duplicate search hit sends the two planes to
	// different providers for the same film.
	results, err := fanOutPreferred(ctx, s.capabilities.SearchProviders(),
		func(e SearchProviderEntry) bool { return e.Fallback },
		func(ctx context.Context, e SearchProviderEntry) ([]v1.SearchResult, error) {
			settings, err := s.readModuleSettings(ctx, e.ModuleID)
			if err != nil {
				return nil, err
			}
			// A span per provider, inside the fan-out. This is where a
			// waterfall earns its keep: several addons are queried at once and
			// the slow one is invisible in any aggregate.
			//
			// It matters more than usual here because the error below is
			// deliberately swallowed — one unreachable addon must not fail the
			// whole search — so until now a provider that failed every time
			// looked exactly like one that returned nothing. The span is the
			// only place that difference is recorded.
			//
			// The module's context is bound to a separate variable rather than
			// shadowing ctx. moduleSpan rebinds the logger and installs the
			// module's telemetry surface (sdk#5), so anything the Platform
			// does afterwards under that context is recorded as the module's
			// work — and, once the span has ended, recorded beneath a parent
			// that has already closed. The dedup below is Platform work, and
			// "was it us or the addon?" is exactly the question these spans
			// exist to answer.
			mctx, span := moduleSpan(ctx, e.ModuleID, "search")
			resp, err := e.Provider.Search(mctx, v1.SearchRequest{
				Caller: q.Caller, Settings: settings, Text: q.Text, MediaType: q.MediaType, Limit: q.Limit,
			})
			failSpan(span, err)
			if err != nil {
				span.End()
				return nil, nil
			}
			span.SetAttributes(telemetry.Int("results", len(resp.Results)))
			span.End()
			out := resp.Results
			for i := range out {
				out[i].InLibrary, out[i].NodeID = s.resolveInLibrary(ctx, az, out[i].Ref)
			}
			return out, nil
		})
	if err != nil {
		return SearchAvailableContentResult{}, err
	}
	return SearchAvailableContentResult{Results: results}, nil
}

// resolveInLibrary reports whether a virtual item's ref already resolves to a
// library Work, and that Work's id — the dedup that marks a virtual result as
// already owned (platform#18). It matches on the provider identity the ref carries;
// a ref without one is never in the library. A lookup error is treated as "not
// found" so a transient read does not falsely hide an item from search.
//
// az is not read here, and that is the point: it is a proof obligation rather
// than data. Requiring it is what makes this an inside-the-boundary read that
// can go straight to the store, instead of the entry point it used to call.
// It ran once per search result, and FindContentByExternalID is a public
// entry point, so a ten-result search re-authenticated and re-authorised ten
// times over — for the same caller, the same action and the same resource the
// handler above had already cleared. Roughly thirty of the search's thirty-nine
// queries were that, and none of them could have reached a different verdict.
func (s *Service) resolveInLibrary(ctx context.Context, az authorized, ref v1.ContentRef) (bool, v1.NodeID) {
	if ref.ExternalScheme == "" || ref.ExternalID == "" {
		return false, ""
	}
	nodes, err := s.nodes.FindByExternalID(ctx, ref.ExternalScheme, ref.ExternalID)
	if err != nil {
		return false, ""
	}
	for _, n := range nodes {
		if n.IsRoot() {
			return true, n.ID
		}
	}
	return false, ""
}
