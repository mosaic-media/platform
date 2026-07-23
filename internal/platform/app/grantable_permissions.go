// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"sort"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// GrantablePermissionsQuery asks what a grantor may offer when creating an
// account.
type GrantablePermissionsQuery struct {
	Caller v1.Caller
	// Preset names the starting selection — PresetNameAdministrator or
	// PresetNameUser. An unknown preset is InvalidArgument rather than an empty
	// selection, since silently offering nothing looks like a broken screen.
	Preset string
}

// GrantablePermissionsResult is what the account-creation screen renders.
type GrantablePermissionsResult struct {
	// Available is every permission this grantor may confer: exactly what they
	// hold themselves. It is the full set of checkboxes the screen shows.
	Available []policy.Action
	// Selected is the subset ticked to begin with: the preset, narrowed to
	// what the grantor actually holds.
	Selected []policy.Action
}

// GrantablePermissions returns the permissions a grantor may confer, and which
// of them a preset starts with (ADR 0069).
//
// The narrowing is the point. **A grantor never sees a permission they do not
// hold** — not greyed out, not disabled, absent — because the list is computed
// from their own grants rather than filtered on the client. An interface that
// shows a box it will refuse to honour teaches people the product is broken,
// and one that shows a box it *would* honour is the escalation this whole rule
// exists to prevent.
//
// It is the offer side of the same invariant delegation.go enforces. Neither
// replaces the other: this decides what is displayed, and the check at
// CreateRole decides what is accepted — because the command surface is
// reachable without going through any screen at all.
func (s *Service) GrantablePermissions(ctx context.Context, q GrantablePermissionsQuery) (GrantablePermissionsResult, error) {
	preset, ok := Preset(q.Preset)
	if !ok {
		return GrantablePermissionsResult{}, contracts.NewError(contracts.InvalidArgument,
			"no permission preset named "+q.Preset)
	}

	// Creating an account means creating and granting a role, so this is gated
	// on the same authority as doing it.
	az, err := s.enter(ctx, q.Caller, ActionRoleCreate, policy.Resource{Type: "role"})
	if err != nil {
		return GrantablePermissionsResult{}, err
	}

	held, err := s.effectivePermissions(ctx, az.userID)
	if err != nil {
		return GrantablePermissionsResult{}, err
	}

	available := make([]policy.Action, 0, len(held))
	for p := range held {
		available = append(available, policy.Action(p))
	}

	selected := make([]policy.Action, 0, len(preset))
	for _, a := range preset {
		if held[domain.Permission(a)] {
			selected = append(selected, a)
		}
	}

	// Sorted, so the screen is stable between renders. Iterating a map would
	// otherwise reshuffle the checkboxes every time the page was opened.
	sortActions(available)
	sortActions(selected)
	return GrantablePermissionsResult{Available: available, Selected: selected}, nil
}

func sortActions(a []policy.Action) {
	sort.Slice(a, func(i, j int) bool { return a[i] < a[j] })
}
