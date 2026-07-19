package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// GetContentNodeQuery reads a single node, optionally with its direct
// children.
type GetContentNodeQuery struct {
	CallerSessionID domain.SessionID
	NodeID          domain.NodeID
	// WithChildren also returns the node's direct children in order. It is
	// one level, not a subtree: variable depth means a caller walks
	// deliberately rather than pulling an unbounded tree by accident.
	WithChildren bool
}

// GetContentNodeResult is the Platform result type returned by
// GetContentNode. Children is nil unless the query asked for it, and empty
// for a node that has none.
type GetContentNodeResult struct {
	Node     domain.Node
	Children []domain.Node
}

func validateGetContentNodeQuery(query GetContentNodeQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.NodeID == "" {
		return contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	return nil
}

// GetContentNode reads one position in the containment tree.
//
// It deliberately does not resolve a whole work: nothing may assume a node's
// children are containers or that the tree has a fixed depth (ADR 0013), so
// a caller descends one indexed step at a time rather than being handed a
// shape it then has to guess the meaning of.
func (s *Service) GetContentNode(ctx context.Context, query GetContentNodeQuery) (GetContentNodeResult, error) {
	// 1. validate query shape.
	if err := validateGetContentNodeQuery(query); err != nil {
		return GetContentNodeResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return GetContentNodeResult{}, err
	}

	// 3. authorize action through policy.
	resource := policy.Resource{Type: "content", ID: string(query.NodeID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRead, resource, policy.PolicyContext{}); err != nil {
		return GetContentNodeResult{}, err
	}

	// 4. load state through a read contract.
	node, err := s.nodes.FindByID(ctx, query.NodeID)
	if err != nil {
		return GetContentNodeResult{}, err
	}

	result := GetContentNodeResult{Node: node}
	if query.WithChildren {
		children, err := s.nodes.ListChildren(ctx, node.ID)
		if err != nil {
			return GetContentNodeResult{}, err
		}
		// Never nil when asked for, so a caller can range without a guard.
		if children == nil {
			children = []domain.Node{}
		}
		result.Children = children
	}
	return result, nil
}
