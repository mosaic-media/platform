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

// GetRoleByNameQuery reads the role a name refers to.
//
// A role's name is unique across the install, so creating the User role can only
// happen once: provisioning a second account from the same preset must find the
// role the first one made rather than fail, or the account is created holding no
// authority and cannot sign in.
type GetRoleByNameQuery struct {
	Caller v1.Caller
	Name   string
}

// GetRoleByNameResult carries the role, when there is one.
type GetRoleByNameResult struct {
	Role domain.Role
	// Found is false when no role has that name. Reported rather than returned
	// as NotFound because the caller's next step is to create it, and a
	// find-or-create written against an error is a find-or-create that swallows
	// the errors that are not "absent".
	Found bool
}

// GetRoleByName resolves a role by its name.
//
// It authorises permission.read — the same action that reads a person's roles
// and their effective set — because it answers the same kind of question about
// the same objects. It is deliberately not gated on role.create: reading what a
// role carries is not the authority to hand it out, and the grant itself is
// bounded by what the grantor holds whatever this returns (platform#44).
func (s *Service) GetRoleByName(ctx context.Context, q GetRoleByNameQuery) (GetRoleByNameResult, error) {
	// 1. validate query shape.
	if q.Caller.Session == "" {
		return GetRoleByNameResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.Name == "" {
		return GetRoleByNameResult{}, contracts.NewError(contracts.InvalidArgument, "a role name is required")
	}

	// 2-3. authenticate the caller and authorize the action.
	if _, err := s.enter(ctx, q.Caller, ActionPermissionRead, policy.Resource{Type: "role"}); err != nil {
		return GetRoleByNameResult{}, err
	}

	role, err := s.permissions.FindRoleByName(ctx, q.Name)
	if err != nil {
		if contracts.CategoryOf(err) == contracts.NotFound {
			return GetRoleByNameResult{}, nil
		}
		return GetRoleByNameResult{}, err
	}
	return GetRoleByNameResult{Role: role, Found: true}, nil
}
