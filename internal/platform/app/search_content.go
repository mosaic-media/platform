// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionContentRead is the policy action evaluated for content reads.
const ActionContentRead policy.Action = "content.read"

// defaultSearchLimit applies when a caller does not specify one, and
// maxSearchLimit caps what it may ask for. Search is user-facing, so an
// unbounded read is a denial of service against a large library rather than
// a convenience.
const (
	defaultSearchLimit = 50
	maxSearchLimit     = 200
)

func validateSearchContentQuery(query v1.SearchContentQuery) error {
	if query.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if query.Kind != "" &&
		query.Kind != v1.NodeWork &&
		query.Kind != v1.NodeContainer &&
		query.Kind != v1.NodeItem {
		return contracts.NewError(contracts.InvalidArgument, "unknown node kind")
	}
	// Rejected at the boundary as well as in the store. A malformed filter is a
	// caller's mistake and should be reported as one before a connection is
	// taken, and validating only in the store would leave a second entry point
	// free to pass it through unchecked.
	if len(query.AttributesContain) > 0 && !json.Valid(query.AttributesContain) {
		return contracts.NewError(contracts.InvalidArgument, "attributes filter must be a valid JSON document")
	}
	return nil
}

// SearchContent answers "do I already have this?" — the read a capability
// makes before sourcing anything, and the one a browse surface makes for a
// user typing into a search box.
//
// It is a query, so it uses a direct read contract rather than a UnitOfWork,
// but it still authenticates and passes through policy before reading state.
func (s *Service) SearchContent(ctx context.Context, query v1.SearchContentQuery) (v1.SearchContentResult, error) {
	// 1. validate query shape.
	if err := validateSearchContentQuery(query); err != nil {
		return v1.SearchContentResult{}, err
	}

	// 2-3. authenticate the caller and authorize the action.
	if _, err := s.enter(ctx, query.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return v1.SearchContentResult{}, err
	}

	// 4. load state through a read contract.
	nodes, err := s.nodes.Search(ctx, contracts.NodeQuery{
		Title:             query.Title,
		MediaType:         query.MediaType,
		Kind:              query.Kind,
		AttributesContain: query.AttributesContain,
		Limit:             clampSearchLimit(query.Limit),
	})
	if err != nil {
		return v1.SearchContentResult{}, err
	}

	return v1.SearchContentResult{Nodes: nodes}, nil
}

// FindContentByExternalID is the strong form of "do I already have this":
// it does not depend on titles matching, which is what makes it the read a
// metadata capability reaches for first.
func (s *Service) FindContentByExternalID(ctx context.Context, query v1.FindContentByExternalIDQuery) (v1.FindContentByExternalIDResult, error) {
	if query.Caller.Session == "" {
		return v1.FindContentByExternalIDResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if query.Scheme == "" {
		return v1.FindContentByExternalIDResult{}, contracts.NewError(contracts.InvalidArgument, "external id scheme is required")
	}
	if query.Value == "" {
		return v1.FindContentByExternalIDResult{}, contracts.NewError(contracts.InvalidArgument, "external id value is required")
	}

	// 2-3. authenticate the caller and authorize the action.
	if _, err := s.enter(ctx, query.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return v1.FindContentByExternalIDResult{}, err
	}

	nodes, err := s.nodes.FindByExternalID(ctx, query.Scheme, query.Value)
	if err != nil {
		return v1.FindContentByExternalIDResult{}, err
	}

	return v1.FindContentByExternalIDResult{Nodes: nodes}, nil
}

func clampSearchLimit(limit int) int {
	switch {
	case limit <= 0:
		return defaultSearchLimit
	case limit > maxSearchLimit:
		return maxSearchLimit
	default:
		return limit
	}
}
