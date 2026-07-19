package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
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

// SearchContentQuery finds content by title, media type and kind. Every
// filter is optional; the empty query returns the first page of everything.
type SearchContentQuery struct {
	CallerSessionID domain.SessionID
	Title           string
	MediaType       domain.MediaType
	Kind            domain.NodeKind
	// Limit is clamped to maxSearchLimit and defaults to
	// defaultSearchLimit when zero or negative.
	Limit int
}

// SearchContentResult is the Platform result type returned by SearchContent.
type SearchContentResult struct {
	Nodes []domain.Node
}

func validateSearchContentQuery(query SearchContentQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.Kind != "" &&
		query.Kind != domain.NodeWork &&
		query.Kind != domain.NodeContainer &&
		query.Kind != domain.NodeItem {
		return contracts.NewError(contracts.InvalidArgument, "unknown node kind")
	}
	return nil
}

// SearchContent answers "do I already have this?" — the read a capability
// makes before sourcing anything, and the one a browse surface makes for a
// user typing into a search box.
//
// It is a query, so it uses a direct read contract rather than a UnitOfWork,
// but it still authenticates and passes through policy before reading state.
func (s *Service) SearchContent(ctx context.Context, query SearchContentQuery) (SearchContentResult, error) {
	// 1. validate query shape.
	if err := validateSearchContentQuery(query); err != nil {
		return SearchContentResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return SearchContentResult{}, err
	}

	// 3. authorize action through policy.
	resource := policy.Resource{Type: "content"}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRead, resource, policy.PolicyContext{}); err != nil {
		return SearchContentResult{}, err
	}

	// 4. load state through a read contract.
	nodes, err := s.nodes.Search(ctx, contracts.NodeQuery{
		Title:     query.Title,
		MediaType: query.MediaType,
		Kind:      query.Kind,
		Limit:     clampSearchLimit(query.Limit),
	})
	if err != nil {
		return SearchContentResult{}, err
	}

	return SearchContentResult{Nodes: nodes}, nil
}

// FindContentByExternalIDQuery resolves content by a provider's identifier.
type FindContentByExternalIDQuery struct {
	CallerSessionID domain.SessionID
	Scheme          string
	Value           string
}

// FindContentByExternalIDResult is the Platform result type returned by
// FindContentByExternalID. More than one node may carry one external id, so
// this is a list rather than a single node — an anime and its source manga
// can share a provider reference and remain two Works (ADR 0013).
type FindContentByExternalIDResult struct {
	Nodes []domain.Node
}

func validateFindContentByExternalIDQuery(query FindContentByExternalIDQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.Scheme == "" {
		return contracts.NewError(contracts.InvalidArgument, "external id scheme is required")
	}
	if query.Value == "" {
		return contracts.NewError(contracts.InvalidArgument, "external id value is required")
	}
	return nil
}

// FindContentByExternalID is the strong form of "do I already have this":
// it does not depend on titles matching, which is what makes it the read a
// metadata capability reaches for first.
func (s *Service) FindContentByExternalID(ctx context.Context, query FindContentByExternalIDQuery) (FindContentByExternalIDResult, error) {
	if err := validateFindContentByExternalIDQuery(query); err != nil {
		return FindContentByExternalIDResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return FindContentByExternalIDResult{}, err
	}

	resource := policy.Resource{Type: "content"}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRead, resource, policy.PolicyContext{}); err != nil {
		return FindContentByExternalIDResult{}, err
	}

	nodes, err := s.nodes.FindByExternalID(ctx, query.Scheme, query.Value)
	if err != nil {
		return FindContentByExternalIDResult{}, err
	}

	return FindContentByExternalIDResult{Nodes: nodes}, nil
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
