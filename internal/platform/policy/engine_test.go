// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package policy_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

type fakePermissionStore struct {
	roles map[domain.UserID][]domain.Role
}

func (s fakePermissionStore) RolesForUser(_ context.Context, userID domain.UserID) ([]domain.Role, error) {
	return s.roles[userID], nil
}

func (s fakePermissionStore) GrantsForUser(context.Context, domain.UserID) ([]domain.Grant, error) {
	return nil, nil
}

func (fakePermissionStore) CreateRole(context.Context, domain.Role) (domain.Role, error) {
	return domain.Role{}, nil
}

func (fakePermissionStore) GrantRole(context.Context, domain.Grant) error { return nil }
func (fakePermissionStore) SetRolePermissions(context.Context, domain.RoleID, []domain.Permission) error {
	return nil
}

func (s fakePermissionStore) AttributesForUser(context.Context, domain.UserID) ([]domain.Attribute, error) {
	return nil, nil
}

func TestEngineAllowsWhenRoleGrantsAction(t *testing.T) {
	store := fakePermissionStore{roles: map[domain.UserID][]domain.Role{
		"user-admin": {{ID: "role-admin", Name: "Administrator", Permissions: []domain.Permission{"user.create"}}},
	}}
	engine := policy.NewEngine(store)

	decision, err := engine.Authorize(context.Background(), policy.Subject{UserID: "user-admin"}, "user.create", policy.Resource{}, policy.PolicyContext{})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if !decision.Allowed {
		t.Fatalf("decision.Allowed = false, want true (reason: %s)", decision.Reason)
	}
}

func TestEngineDeniesWhenNoRoleGrantsAction(t *testing.T) {
	store := fakePermissionStore{roles: map[domain.UserID][]domain.Role{
		"user-viewer": {{ID: "role-viewer", Name: "Viewer", Permissions: []domain.Permission{"user.read"}}},
	}}
	engine := policy.NewEngine(store)

	decision, err := engine.Authorize(context.Background(), policy.Subject{UserID: "user-viewer"}, "user.create", policy.Resource{}, policy.PolicyContext{})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("decision.Allowed = true, want false")
	}
}

func TestEngineDeniesUnknownSubject(t *testing.T) {
	engine := policy.NewEngine(fakePermissionStore{})

	decision, err := engine.Authorize(context.Background(), policy.Subject{UserID: "user-nobody"}, "user.create", policy.Resource{}, policy.PolicyContext{})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("decision.Allowed = true, want false")
	}
}

func TestEngineDeniesEmptySubject(t *testing.T) {
	engine := policy.NewEngine(fakePermissionStore{})

	decision, err := engine.Authorize(context.Background(), policy.Subject{}, "user.create", policy.Resource{}, policy.PolicyContext{})
	if err != nil {
		t.Fatalf("Authorize() error = %v", err)
	}
	if decision.Allowed {
		t.Fatal("decision.Allowed = true, want false")
	}
}

func TestEngineTranslatesPermissionStoreFailure(t *testing.T) {
	engine := policy.NewEngine(failingPermissionStore{})

	_, err := engine.Authorize(context.Background(), policy.Subject{UserID: "user-admin"}, "user.create", policy.Resource{}, policy.PolicyContext{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if got := contracts.CategoryOf(err); got != contracts.Internal {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.Internal)
	}
}

type failingPermissionStore struct{}

func (failingPermissionStore) RolesForUser(context.Context, domain.UserID) ([]domain.Role, error) {
	return nil, contracts.NewError(contracts.Unavailable, "permission store unreachable")
}

func (failingPermissionStore) GrantsForUser(context.Context, domain.UserID) ([]domain.Grant, error) {
	return nil, nil
}

func (failingPermissionStore) CreateRole(context.Context, domain.Role) (domain.Role, error) {
	return domain.Role{}, nil
}

func (failingPermissionStore) GrantRole(context.Context, domain.Grant) error { return nil }
func (failingPermissionStore) SetRolePermissions(context.Context, domain.RoleID, []domain.Permission) error {
	return nil
}

func (failingPermissionStore) AttributesForUser(context.Context, domain.UserID) ([]domain.Attribute, error) {
	return nil, nil
}

func (fakePermissionStore) FindRole(context.Context, domain.RoleID) (domain.Role, error) {
	return domain.Role{}, nil
}

func (failingPermissionStore) FindRole(context.Context, domain.RoleID) (domain.Role, error) {
	return domain.Role{}, contracts.NewError(contracts.Unavailable, "permission store unreachable")
}
