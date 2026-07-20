// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"strings"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
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

	var results []v1.SearchResult
	for _, e := range s.capabilities.SearchProviders() {
		settings, err := s.readModuleSettings(ctx, e.ModuleID)
		if err != nil {
			return SearchAvailableContentResult{}, err
		}
		resp, err := e.Provider.Search(ctx, v1.SearchRequest{
			Caller: q.Caller, Settings: settings, Text: q.Text, MediaType: q.MediaType, Limit: q.Limit,
		})
		if err != nil {
			// A provider being unreachable or misconfigured empties its plane;
			// the rest of the union still stands.
			continue
		}
		for _, r := range resp.Results {
			r.InLibrary, r.NodeID = s.resolveInLibrary(ctx, q.Caller, r.Ref)
			results = append(results, r)
		}
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
