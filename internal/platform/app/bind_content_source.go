package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// ActionContentBind is the policy action evaluated for binding a source to a
// node.
const ActionContentBind policy.Action = "content.bind"

func validateBindContentSourceCommand(cmd v1.BindContentSourceCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.NodeID == "" {
		return contracts.NewError(contracts.InvalidArgument, "node id is required")
	}
	if cmd.SourceProvider == "" || cmd.SourceRef == "" {
		return contracts.NewError(contracts.InvalidArgument, "source provider and reference are required")
	}
	if cmd.MatchConfidence < 0 || cmd.MatchConfidence > 1 {
		return contracts.NewError(contracts.InvalidArgument, "match confidence must be between 0 and 1")
	}
	if !knownMatchMethod(cmd.MatchMethod) {
		return contracts.NewError(contracts.InvalidArgument, "unknown match method")
	}
	// A binding is created either confirmed or queued for review. Rejected is
	// a resolution of an existing binding, not a state to create one in.
	if cmd.Status != v1.BindingConfirmed && cmd.Status != v1.BindingPendingReview {
		return contracts.NewError(contracts.InvalidArgument, "a new binding is confirmed or pending_review")
	}
	return nil
}

// BindContentSource records that a source resolves to a node. Identity
// resolution is a visible act (ADR 0013): a strong match binds confirmed, a
// weak one as pending_review so a person sees it rather than two works
// silently merging because they share a title.
func (s *Service) BindContentSource(ctx context.Context, cmd v1.BindContentSourceCommand) (v1.BindContentSourceResult, error) {
	// 1. validate command shape.
	if err := validateBindContentSourceCommand(cmd); err != nil {
		return v1.BindContentSourceResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return v1.BindContentSourceResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentBind, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return v1.BindContentSourceResult{}, err
	}

	var result v1.BindContentSourceResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. the node must exist. A binding to nothing is not identity
		// resolution, it is a dangling row.
		if _, err := tx.Nodes().FindByID(ctx, cmd.NodeID); err != nil {
			return err
		}

		now := s.clock.Now()
		binding := v1.SourceBinding{
			ID:              v1.SourceBindingID(s.contentIDs.NewID()),
			NodeID:          cmd.NodeID,
			SourceProvider:  cmd.SourceProvider,
			SourceRef:       cmd.SourceRef,
			MatchConfidence: cmd.MatchConfidence,
			MatchMethod:     cmd.MatchMethod,
			Status:          cmd.Status,
			CreatedAt:       now,
			UpdatedAt:       now,
		}

		// 7. persist state and the outbox event in the same transaction. A
		// duplicate (provider, ref) surfaces as Conflict from the store —
		// one source binds to at most one node.
		created, err := tx.SourceBindings().Create(ctx, binding)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("content.source.bound", []byte(string(created.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = v1.BindContentSourceResult{Binding: created}
		return nil
	})
	if err != nil {
		return v1.BindContentSourceResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}

func knownMatchMethod(m v1.MatchMethod) bool {
	switch m {
	case v1.MatchExternalIDExact, v1.MatchFingerprint,
		v1.MatchFuzzyTitle, v1.MatchUserSelected:
		return true
	default:
		return false
	}
}
