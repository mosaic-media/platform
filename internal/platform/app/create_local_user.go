package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// ActionUserCreate is the policy action evaluated for CreateLocalUser.
const ActionUserCreate Action = "user.create"

// CreateLocalUserCommand provisions a local Platform user account. It is
// an administrative operation (MEG-009 §04 — Administrative Operations):
// CallerSessionID must belong to an already-authenticated, authorized
// caller, not the new user being created.
type CreateLocalUserCommand struct {
	CallerSessionID domain.SessionID
	Username        string
	Email           string
	DisplayName     string
}

// CreateLocalUserResult is the Platform result type returned once the new
// user has committed.
type CreateLocalUserResult struct {
	User domain.User
}

func validateCreateLocalUserCommand(cmd CreateLocalUserCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.Username == "" {
		return contracts.NewError(contracts.InvalidArgument, "username is required")
	}
	if cmd.Email == "" {
		return contracts.NewError(contracts.InvalidArgument, "email is required")
	}
	return nil
}

// CreateLocalUser implements the command boundary from MEG-015 §04:
// validate shape, authenticate, authorize, open a UnitOfWork, load state,
// apply the domain rule (username uniqueness), persist the new user and
// its outbox event in the same transaction, then return a Platform result.
func (s *Service) CreateLocalUser(ctx context.Context, cmd CreateLocalUserCommand) (CreateLocalUserResult, error) {
	// 1. validate command shape.
	if err := validateCreateLocalUserCommand(cmd); err != nil {
		return CreateLocalUserResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return CreateLocalUserResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, Subject{UserID: callerID}, ActionUserCreate, Resource{Type: "user"}); err != nil {
		return CreateLocalUserResult{}, err
	}

	var result CreateLocalUserResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5. load state through contracts.
		_, err := tx.Users().FindByUsername(ctx, cmd.Username)
		switch {
		case err == nil:
			// 6. apply domain rules: usernames must be unique.
			return contracts.NewError(contracts.Conflict, "username already exists")
		case contracts.CategoryOf(err) != contracts.NotFound:
			return err
		}

		now := s.clock.Now()
		newUser := domain.User{
			ID:          domain.UserID(s.ids.NewID()),
			Username:    cmd.Username,
			Email:       cmd.Email,
			DisplayName: cmd.DisplayName,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		// 7. persist state and outbox events in the same transaction.
		created, err := tx.Users().Create(ctx, newUser)
		if err != nil {
			return err
		}

		event := domain.OutboxEvent{
			Event: domain.Event{
				ID:         domain.EventID(s.ids.NewID()),
				Type:       "user.created",
				Payload:    []byte(created.Username),
				OccurredAt: now,
			},
		}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}

		result = CreateLocalUserResult{User: created}
		return nil
	})
	if err != nil {
		return CreateLocalUserResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
