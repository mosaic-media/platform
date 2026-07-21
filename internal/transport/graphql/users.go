// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql

import (
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// usersField is the Users list query. It calls
// app.Service.ListUsers only.
func usersField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: graphql.NewList(userType),
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ListUsers(p.Context, app.ListUsersQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Users, nil
		},
	}
}

// userField is the Users detail query. It calls
// app.Service.GetUserByID only.
func userField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: userType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"id":              &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetUserByID(p.Context, app.GetUserByIDQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				UserID:          domain.UserID(p.Args["id"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.User, nil
		},
	}
}

// setUserStatusField is the admin-managed user-status mutation. It
// calls app.Service.SetUserStatus only.
func setUserStatusField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: userType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"userId":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"status":          &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.SetUserStatus(p.Context, app.SetUserStatusCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				TargetUserID:    domain.UserID(p.Args["userId"].(string)),
				Status:          domain.UserStatus(p.Args["status"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.User, nil
		},
	}
}
