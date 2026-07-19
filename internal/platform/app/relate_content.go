package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// ActionContentRelate is the policy action evaluated for writing an edge in
// the association graph.
const ActionContentRelate policy.Action = "content.relate"

// RelateContentCommand draws a typed, directed edge between two works — an
// adaptation, a sequel, a collection membership (ADR 0013).
//
// Association is a graph and does not nest, which is why it lives here rather
// than in the containment tree. This is also where three of ADR 0013's four
// non-uniformities are expressed: an artist joined to its albums, a collected
// edition to what it collects, an anime to its source manga.
type RelateContentCommand struct {
	CallerSessionID domain.SessionID
	FromNodeID      domain.NodeID
	ToNodeID        domain.NodeID
	Type            domain.RelationType
	// Confidence is between 0 and 1. The Origin says where the assertion
	// came from, which is what makes a low confidence actionable.
	Confidence float64
	Origin     domain.RelationOrigin
}

// RelateContentResult carries the committed edge.
type RelateContentResult struct {
	Relation domain.Relation
}

func validateRelateContentCommand(cmd RelateContentCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
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

// RelateContent writes one edge of the association graph.
func (s *Service) RelateContent(ctx context.Context, cmd RelateContentCommand) (RelateContentResult, error) {
	// 1. validate command shape.
	if err := validateRelateContentCommand(cmd); err != nil {
		return RelateContentResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return RelateContentResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRelate, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return RelateContentResult{}, err
	}

	var result RelateContentResult

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

		relation := domain.Relation{
			ID:         domain.RelationID(s.contentIDs.NewID()),
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

		result = RelateContentResult{Relation: created}
		return nil
	})
	if err != nil {
		return RelateContentResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}

func knownRelationType(t domain.RelationType) bool {
	switch t {
	case domain.RelationAdaptation, domain.RelationSequel, domain.RelationPrequel,
		domain.RelationSpinoff, domain.RelationCollectionMember,
		domain.RelationAlternateEditionOf, domain.RelationSameFranchise:
		return true
	default:
		return false
	}
}

func knownRelationOrigin(o domain.RelationOrigin) bool {
	switch o {
	case domain.OriginSystemInferred, domain.OriginProviderSupplied, domain.OriginUserConfirmed:
		return true
	default:
		return false
	}
}
