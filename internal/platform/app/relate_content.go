// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionContentRelate is the policy action evaluated for writing an edge in
// the association graph.
const ActionContentRelate policy.Action = "content.relate"

func validateRelateContentCommand(cmd v1.RelateContentCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.FromNodeID == "" || cmd.ToNodeID == "" {
		return contracts.NewError(contracts.InvalidArgument, "both endpoints are required")
	}
	if cmd.FromNodeID == cmd.ToNodeID {
		return contracts.NewError(contracts.InvalidArgument, "a relation cannot join a node to itself")
	}
	if !knownRelationType(cmd.Type) {
		return contracts.NewError(contracts.InvalidArgument, "unknown relation type")
	}
	if cmd.Confidence < 0 || cmd.Confidence > 1 {
		return contracts.NewError(contracts.InvalidArgument, "confidence must be between 0 and 1")
	}
	if !knownRelationOrigin(cmd.Origin) {
		return contracts.NewError(contracts.InvalidArgument, "unknown relation origin")
	}
	return nil
}

// RelateContent writes one edge of the association graph. Association is a
// graph and does not nest (ADR 0013); this is where three of the four
// deliberate non-uniformities are expressed — an artist joined to its albums,
// a collected edition to what it collects, an anime to its source manga.
func (s *Service) RelateContent(ctx context.Context, cmd v1.RelateContentCommand) (v1.RelateContentResult, error) {
	// 1. validate command shape.
	if err := validateRelateContentCommand(cmd); err != nil {
		return v1.RelateContentResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return v1.RelateContentResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRelate, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return v1.RelateContentResult{}, err
	}

	var result v1.RelateContentResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. both endpoints must exist. Loading them turns a dangling edge
		// into a NotFound the caller can act on, rather than a foreign-key
		// violation surfacing as a Conflict.
		if _, err := tx.Nodes().FindByID(ctx, cmd.FromNodeID); err != nil {
			return err
		}
		if _, err := tx.Nodes().FindByID(ctx, cmd.ToNodeID); err != nil {
			return err
		}

		relation := v1.Relation{
			ID:         v1.RelationID(s.contentIDs.NewID()),
			FromNodeID: cmd.FromNodeID,
			ToNodeID:   cmd.ToNodeID,
			Type:       cmd.Type,
			Confidence: cmd.Confidence,
			Origin:     cmd.Origin,
			CreatedAt:  s.clock.Now(),
		}

		// 7. persist state and the outbox event in the same transaction.
		created, err := tx.Relations().Create(ctx, relation)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("content.relation.created", []byte(string(created.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = v1.RelateContentResult{Relation: created}
		return nil
	})
	if err != nil {
		return v1.RelateContentResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}

func knownRelationType(t v1.RelationType) bool {
	switch t {
	case v1.RelationAdaptation, v1.RelationSequel, v1.RelationPrequel,
		v1.RelationSpinoff, v1.RelationCollectionMember,
		v1.RelationAlternateEditionOf, v1.RelationSameFranchise:
		return true
	default:
		return false
	}
}

func knownRelationOrigin(o v1.RelationOrigin) bool {
	switch o {
	case v1.OriginSystemInferred, v1.OriginProviderSupplied, v1.OriginUserConfirmed:
		return true
	default:
		return false
	}
}
