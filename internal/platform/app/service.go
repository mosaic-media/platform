package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// Service hosts Platform application command and query handlers. It holds
// direct read access to SessionStore and UserStore for authentication and
// query paths, and a UnitOfWork for the transactional write path — the
// same contracts, reached through the shape appropriate to each operation
// (MEG-015 §04).
type Service struct {
	uow      contracts.UnitOfWork
	sessions contracts.SessionStore
	users    contracts.UserStore
	clock    contracts.Clock
	ids      contracts.IDGenerator
	policy   PolicyDecisionPoint
}

// NewService wires a Service to its Platform contracts and policy
// decision point.
func NewService(
	uow contracts.UnitOfWork,
	sessions contracts.SessionStore,
	users contracts.UserStore,
	clock contracts.Clock,
	ids contracts.IDGenerator,
	policy PolicyDecisionPoint,
) *Service {
	return &Service{
		uow:      uow,
		sessions: sessions,
		users:    users,
		clock:    clock,
		ids:      ids,
		policy:   policy,
	}
}

// authenticate resolves the caller identity behind sessionID. It is step 2
// of the command boundary (MEG-015 §04) and the equivalent gate for
// queries: it runs before any policy or state check, and failure stops
// processing immediately (MEG-009 §03 — Authentication).
func (s *Service) authenticate(ctx context.Context, sessionID domain.SessionID) (domain.UserID, error) {
	if sessionID == "" {
		return "", contracts.NewError(contracts.Unauthenticated, "missing caller session")
	}

	session, err := s.sessions.FindByID(ctx, sessionID)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return "", contracts.WrapError(contracts.Unauthenticated, "session not found", err)
		}
		return "", err
	}

	if session.Revoked() {
		return "", contracts.NewError(contracts.Unauthenticated, "session revoked")
	}
	if session.ExpiredAt(s.clock.Now()) {
		return "", contracts.NewError(contracts.Unauthenticated, "session expired")
	}

	return session.UserID, nil
}

// authorize resolves step 3 of the command boundary (and the equivalent
// query gate): it asks the PolicyDecisionPoint whether subject may perform
// action on resource, and translates a denial into a PermissionDenied
// contract error.
func (s *Service) authorize(ctx context.Context, subject Subject, action Action, resource Resource) error {
	decision, err := s.policy.Authorize(ctx, subject, action, resource, PolicyContext{})
	if err != nil {
		return contracts.WrapError(contracts.Internal, "evaluate policy", err)
	}
	if !decision.Allowed {
		return contracts.NewError(contracts.PermissionDenied, decision.Reason)
	}
	return nil
}
