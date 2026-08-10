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

func validateGetContentNodeQuery(query v1.GetContentNodeQuery) error {
	if query.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if query.NodeID == "" {
		return contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	return nil
}

// GetContentNode reads one position in the containment tree.
//
// It deliberately does not resolve a whole work: nothing may assume a node's
// children are containers or that the tree has a fixed depth (platform#9), so
// a caller descends one indexed step at a time rather than being handed a
// shape it then has to guess the meaning of.
func (s *Service) GetContentNode(ctx context.Context, query v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	// 1. validate query shape.
	if err := validateGetContentNodeQuery(query); err != nil {
		return v1.GetContentNodeResult{}, err
	}

	// 2-3. authenticate the caller and authorize the action.
	if _, err := s.enter(ctx, query.Caller, ActionContentRead,
		policy.Resource{Type: "content", ID: string(query.NodeID)}); err != nil {
		return v1.GetContentNodeResult{}, err
	}

	// 4. load state through a read contract.
	node, err := s.nodes.FindByID(ctx, query.NodeID)
	if err != nil {
		return v1.GetContentNodeResult{}, err
	}

	result := v1.GetContentNodeResult{Node: node}
	if query.WithChildren {
		children, err := s.nodes.ListChildren(ctx, node.ID)
		if err != nil {
			return v1.GetContentNodeResult{}, err
		}
		// Never nil when asked for, so a caller can range without a guard.
		if children == nil {
			children = []v1.Node{}
		}
		result.Children = children
	}
	return result, nil
}
