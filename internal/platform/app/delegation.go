// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"sort"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// Privilege cannot escalate through delegation (ADR 0069).
//
// **Nobody may grant authority they do not themselves hold.** It is the one
// rule that makes granular permissions safe to delegate: without it,
// `role.create` is equivalent to every permission, because the holder can mint
// a role carrying anything and grant it to themselves. The action named
// "create a role" would silently be the action "become anyone".
//
// This is enforced in the Platform, not in the interface that offers it. A UI
// that only shows a grantor the boxes they can tick is good design and no
// defence at all — the command surface is reachable directly, and an interface
// is not where an authority boundary can live.

// permissionSet is a caller's effective permissions, for subset comparison.
type permissionSet map[domain.Permission]bool

// effectivePermissions collects every permission a user holds across all their
// roles.
//
// A read of current state rather than anything cached: authority changes, and a
// delegation check against a stale set would let someone grant what they were
// only briefly given.
func (s *Service) effectivePermissions(ctx context.Context, userID domain.UserID) (permissionSet, error) {
	roles, err := s.permissions.RolesForUser(ctx, userID)
	if err != nil {
		return nil, err
	}
	held := make(permissionSet)
	for _, role := range roles {
		for _, p := range role.Permissions {
			held[p] = true
		}
	}
	return held, nil
}

// ensureCanDelegate verifies that granting wanted would not give away authority
// the caller lacks.
//
// The error names what was missing. A refusal that says only "denied" is
// useless to someone assembling a role from twenty checkboxes, and the
// information is not sensitive: the caller already knows their own permissions,
// and they chose the set being refused.
func (s *Service) ensureCanDelegate(ctx context.Context, az authorized, wanted []domain.Permission) error {
	if len(wanted) == 0 {
		return nil
	}
	held, err := s.effectivePermissions(ctx, az.userID)
	if err != nil {
		return err
	}

	var missing []string
	for _, p := range wanted {
		if !held[p] {
			missing = append(missing, string(p))
		}
	}
	if len(missing) == 0 {
		return nil
	}
	// Sorted, so the same refusal reads the same way twice — a set iteration
	// order would otherwise reorder the message between identical attempts.
	sort.Strings(missing)
	return contracts.NewError(contracts.PermissionDenied,
		"cannot grant permissions you do not hold: "+strings.Join(missing, ", "))
}

// ensureCanDelegateRole is ensureCanDelegate for granting an existing role: the
// role's whole permission set must be within the caller's.
//
// Checked at grant time rather than trusted from creation, because the two are
// separate acts. A role created by the superuser can be granted by an
// administrator, and it is the *granting* that must be bounded by what the
// grantor holds — otherwise "grant this existing role" becomes the escalation
// path that creating one was closed against.
func (s *Service) ensureCanDelegateRole(ctx context.Context, az authorized, roleID domain.RoleID) error {
	role, err := s.findRole(ctx, roleID)
	if err != nil {
		// A role that does not exist delegates nothing, so there is nothing to
		// bound. Returning here would also change the error a caller sees for
		// a bad role id — the store reports that as Conflict from its foreign
		// key, and a check added for delegation has no business rewriting an
		// unrelated contract.
		if contracts.CategoryOf(err) == contracts.NotFound {
			return nil
		}
		return err
	}
	return s.ensureCanDelegate(ctx, az, role.Permissions)
}

// findRole resolves a role by id. PermissionStore gained FindRole for this:
// a grant that cannot see what it is granting cannot bound it.
func (s *Service) findRole(ctx context.Context, roleID domain.RoleID) (domain.Role, error) {
	return s.permissions.FindRole(ctx, roleID)
}
