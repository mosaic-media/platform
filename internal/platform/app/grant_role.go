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

// ActionRoleGrant is the policy action evaluated for granting a role to a
// user.
const ActionRoleGrant policy.Action = "role.grant"

// GrantRoleCommand binds an existing role to an existing user, giving that
// user the role's permissions. This is how authority reaches a user.
type GrantRoleCommand struct {
	CallerSessionID domain.SessionID
	UserID          domain.UserID
	RoleID          domain.RoleID
}

// GrantRoleResult carries the committed grant.
type GrantRoleResult struct {
	Grant domain.Grant
}

func validateGrantRoleCommand(cmd GrantRoleCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.UserID == "" {
		return contracts.NewError(contracts.InvalidArgument, "user id is required")
	}
	if cmd.RoleID == "" {
		return contracts.NewError(contracts.InvalidArgument, "role id is required")
	}
	return nil
}

// GrantRole binds a role to a user, following the command boundary.
func (s *Service) GrantRole(ctx context.Context, cmd GrantRoleCommand) (GrantRoleResult, error) {
	// 1. validate command shape.
	if err := validateGrantRoleCommand(cmd); err != nil {
		return GrantRoleResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return GrantRoleResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionRoleGrant, policy.Resource{Type: "role", ID: string(cmd.RoleID)}, policy.PolicyContext{}); err != nil {
		return GrantRoleResult{}, err
	}

	grant := domain.Grant{UserID: cmd.UserID, RoleID: cmd.RoleID}

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 7. persist state and the outbox event in the same transaction. A
		// grant naming a role or user that does not exist, or a duplicate,
		// surfaces as Conflict from the store.
		if err := tx.Permissions().GrantRole(ctx, grant); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("role.granted", []byte(string(cmd.UserID)), string(callerID)),
		})
	})
	if err != nil {
		return GrantRoleResult{}, err
	}

	// 8. return a Platform result type.
	return GrantRoleResult{Grant: grant}, nil
}
