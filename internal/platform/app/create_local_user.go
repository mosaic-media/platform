// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// ActionUserCreate is the policy action evaluated for CreateLocalUser.
const ActionUserCreate policy.Action = "user.create"

// CreateLocalUserCommand provisions a local Platform user account with a
// password credential. It is an administrative operation:
// CallerSessionID must belong to an already-authenticated, authorized
// caller, not the new user being created.
type CreateLocalUserCommand struct {
	CallerSessionID domain.SessionID
	Username        string
	Email           string
	DisplayName     string
	Password        string
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
	if cmd.Password == "" {
		return contracts.NewError(contracts.InvalidArgument, "password is required")
	}
	return nil
}

// CreateLocalUser adds a local account and the password credential it signs in
// with. The account is created active and holds no role: granting one is a
// separate command.
func (s *Service) CreateLocalUser(ctx context.Context, cmd CreateLocalUserCommand) (CreateLocalUserResult, error) {
	if err := validateCreateLocalUserCommand(cmd); err != nil {
		return CreateLocalUserResult{}, err
	}

	az, err := s.enterSession(ctx, cmd.CallerSessionID, ActionUserCreate, policy.Resource{Type: "user"})
	if err != nil {
		return CreateLocalUserResult{}, err
	}

	var result CreateLocalUserResult

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		_, err := tx.Users().FindByUsername(ctx, cmd.Username)
		switch {
		case err == nil:
			// Usernames must be unique.
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
			Status:      domain.UserActive,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		created, err := tx.Users().Create(ctx, newUser)
		if err != nil {
			return err
		}

		hash, err := s.passwordVerifier.Hash(cmd.Password)
		if err != nil {
			return contracts.WrapError(contracts.Internal, "hash password", err)
		}
		credential := domain.PasswordCredential{
			UserID:    created.ID,
			Hash:      hash,
			UpdatedAt: now,
		}
		if err := tx.Credentials().SavePassword(ctx, credential); err != nil {
			return err
		}

		// The actor is the authorized caller who created the account, not the
		// new user.
		event := domain.OutboxEvent{Event: s.newEvent(ctx, "user.created", []byte(created.Username), string(az.userID))}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}

		result = CreateLocalUserResult{User: created}
		return nil
	})
	if err != nil {
		return CreateLocalUserResult{}, err
	}

	return result, nil
}
