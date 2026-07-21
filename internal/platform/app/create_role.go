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

// ActionRoleCreate is the policy action evaluated for creating a role.
const ActionRoleCreate policy.Action = "role.create"

// CreateRoleCommand defines a role — a named set of permissions — that can
// then be granted to users. It is an administrative operation: the caller
// must already hold the authority to assign authority.
type CreateRoleCommand struct {
	CallerSessionID domain.SessionID
	Name            string
	Permissions     []string
}

// CreateRoleResult carries the committed role.
type CreateRoleResult struct {
	Role domain.Role
}

func validateCreateRoleCommand(cmd CreateRoleCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.Name == "" {
		return contracts.NewError(contracts.InvalidArgument, "role name is required")
	}
	return nil
}

// CreateRole persists a new role, following the command boundary.
func (s *Service) CreateRole(ctx context.Context, cmd CreateRoleCommand) (CreateRoleResult, error) {
	// 1. validate command shape.
	if err := validateCreateRoleCommand(cmd); err != nil {
		return CreateRoleResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return CreateRoleResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionRoleCreate, policy.Resource{Type: "role"}, policy.PolicyContext{}); err != nil {
		return CreateRoleResult{}, err
	}

	var result CreateRoleResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		role := domain.Role{
			ID:          domain.RoleID(s.ids.NewID()),
			Name:        cmd.Name,
			Permissions: toPermissions(cmd.Permissions),
		}

		// 7. persist state and the outbox event in the same transaction.
		created, err := tx.Permissions().CreateRole(ctx, role)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("role.created", []byte(string(created.ID)), string(callerID)),
		}); err != nil {
			return err
		}

		result = CreateRoleResult{Role: created}
		return nil
	})
	if err != nil {
		return CreateRoleResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}

func toPermissions(values []string) []domain.Permission {
	perms := make([]domain.Permission, len(values))
	for i, v := range values {
		perms[i] = domain.Permission(v)
	}
	return perms
}
