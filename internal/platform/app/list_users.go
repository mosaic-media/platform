package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// ActionUserList is the policy action evaluated for ListUsers.
const ActionUserList policy.Action = "user.list"

// ListUsersQuery lists every local Platform user (MEG-015 §09 — Users:
// "user list").
type ListUsersQuery struct {
	CallerSessionID domain.SessionID
}

// ListUsersResult is the Platform result type returned by ListUsers.
type ListUsersResult struct {
	Users []domain.User
}

func validateListUsersQuery(query ListUsersQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	return nil
}

// ListUsers implements the query boundary from MEG-015 §04: authenticate
// and authorize before reading state, no UnitOfWork needed for a read.
func (s *Service) ListUsers(ctx context.Context, query ListUsersQuery) (ListUsersResult, error) {
	if err := validateListUsersQuery(query); err != nil {
		return ListUsersResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return ListUsersResult{}, err
	}

	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionUserList, policy.Resource{Type: "user"}, policy.PolicyContext{}); err != nil {
		return ListUsersResult{}, err
	}

	users, err := s.users.List(ctx)
	if err != nil {
		return ListUsersResult{}, err
	}
	return ListUsersResult{Users: users}, nil
}
