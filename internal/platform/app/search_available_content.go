// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// SearchAvailableContentQuery is a free-text search over what the enabled
// modules can source — the discovery surface that lets a user search Mosaic
// without a raw provider id (ADR 0028). It is a Platform query, not part of the
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

	callerID, err := s.authenticateCaller(ctx, q.Caller)
	if err != nil {
		return SearchAvailableContentResult{}, err
	}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRead, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return SearchAvailableContentResult{}, err
	}

	if s.capabilities == nil {
		return SearchAvailableContentResult{}, nil
	}

	// Fan the query out to every provider concurrently; each is an independent
	// remote round-trip. fanOut preserves provider order and the two error paths:
	// a settings read that fails aborts the query, a provider that is down is
	// skipped (nil, nil) so its plane empties without blanking the others.
	results, err := fanOut(ctx, s.capabilities.SearchProviders(),
		func(ctx context.Context, e SearchProviderEntry) ([]v1.SearchResult, error) {
			settings, err := s.readModuleSettings(ctx, e.ModuleID)
			if err != nil {
				return nil, err
			}
			resp, err := e.Provider.Search(ctx, v1.SearchRequest{
				Caller: q.Caller, Settings: settings, Text: q.Text, MediaType: q.MediaType, Limit: q.Limit,
			})
			if err != nil {
				return nil, nil
			}
			out := resp.Results
			for i := range out {
				out[i].InLibrary, out[i].NodeID = s.resolveInLibrary(ctx, q.Caller, out[i].Ref)
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
// already owned (ADR 0028). It matches on the provider identity the ref carries;
// a ref without one is never in the library. A lookup error is treated as "not
// found" so a transient read does not falsely hide an item from search.
func (s *Service) resolveInLibrary(ctx context.Context, caller v1.Caller, ref v1.ContentRef) (bool, v1.NodeID) {
	if ref.ExternalScheme == "" || ref.ExternalID == "" {
		return false, ""
	}
	found, err := s.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: caller, Scheme: ref.ExternalScheme, Value: ref.ExternalID,
	})
	if err != nil {
		return false, ""
	}
	for _, n := range found.Nodes {
		if n.IsRoot() {
			return true, n.ID
		}
	}
	return false, ""
}
