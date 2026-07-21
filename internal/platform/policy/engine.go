// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package policy

import (
	"context"
	"fmt"

	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// Engine is the first Platform Policy Decision Point. It
// resolves the subject's roles through PermissionStore and allows the
// request if any granted role includes the requested Action as a
// permission. Resource and PolicyContext are accepted and threaded
// through untouched: the ABAC-ready shape must be real now, even though
// only role membership drives this slice's decisions. Relationship- and
// attribute-based rules are expected to extend Authorize's logic later,
// not change its signature.
//
// Unresolvable subjects and subjects with no matching grant are denied,
// matching the default-deny rule.
type Engine struct {
	permissions contracts.PermissionStore
}

// NewEngine builds an Engine backed by permissions.
func NewEngine(permissions contracts.PermissionStore) *Engine {
	return &Engine{permissions: permissions}
}

// Authorize resolves the subject's roles and returns an allow Decision if any
// granted role includes action as a permission; otherwise it denies.
func (e *Engine) Authorize(ctx context.Context, subject Subject, action Action, _ Resource, _ PolicyContext) (Decision, error) {
	if subject.UserID == "" {
		return Decision{Allowed: false, Reason: "no subject"}, nil
	}

	roles, err := e.permissions.RolesForUser(ctx, subject.UserID)
	if err != nil {
		return Decision{}, contracts.WrapError(contracts.Internal, "resolve roles for subject", err)
	}

	for _, role := range roles {
		for _, permission := range role.Permissions {
			if string(permission) == string(action) {
				return Decision{
					Allowed: true,
					Reason:  fmt.Sprintf("role %q grants %q", role.Name, action),
				}, nil
			}
		}
	}

	return Decision{Allowed: false, Reason: fmt.Sprintf("no role grants %q", action)}, nil
}
