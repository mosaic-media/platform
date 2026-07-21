// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql

import (
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// rolesForUserField is the Permissions query "roles". It calls
// app.Service.GetRolesForUser only.
func rolesForUserField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(roleType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"userId":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetRolesForUser(p.Context, app.GetRolesForUserQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				TargetUserID:    domain.UserID(p.Args["userId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Roles, nil
		},
	}
}

// grantsForUserField is the Permissions query "grants". It calls
// app.Service.GetGrantsForUser only.
func grantsForUserField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(grantType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"userId":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetGrantsForUser(p.Context, app.GetGrantsForUserQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				TargetUserID:    domain.UserID(p.Args["userId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Grants, nil
		},
	}
}

// effectivePermissionsField is the Permissions query "effective permission
// inspection". It calls app.Service.GetEffectivePermissions only.
func effectivePermissionsField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(graphql.String),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"userId":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetEffectivePermissions(p.Context, app.GetEffectivePermissionsQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				TargetUserID:    domain.UserID(p.Args["userId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return permissionsToStrings(result.Permissions), nil
		},
	}
}

// createRoleField is the Permissions mutation "create role". It calls
// app.Service.CreateRole only.
func createRoleField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: roleType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"name":            &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"permissions":     &graphql.ArgumentConfig{Type: graphql.NewList(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.CreateRole(p.Context, app.CreateRoleCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				Name:            p.Args["name"].(string),
				Permissions:     stringListArg(p.Args["permissions"]),
			})
			if err != nil {
				return nil, err
			}
			return result.Role, nil
		},
	}
}

// grantRoleField is the Permissions mutation "grant role". It calls
// app.Service.GrantRole only.
func grantRoleField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: grantType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"userId":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"roleId":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GrantRole(p.Context, app.GrantRoleCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				UserID:          domain.UserID(p.Args["userId"].(string)),
				RoleID:          domain.RoleID(p.Args["roleId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Grant, nil
		},
	}
}

// stringListArg converts an optional [String] argument to []string.
func stringListArg(arg interface{}) []string {
	raw, ok := arg.([]interface{})
	if !ok {
		return nil
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			out = append(out, s)
		}
	}
	return out
}
