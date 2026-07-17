package graphql

import (
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// rolesForUserField is the MEG-015 §09 Permissions query "roles". It
// calls app.Service.GetRolesForUser only.
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

// grantsForUserField is the MEG-015 §09 Permissions query "grants". It
// calls app.Service.GetGrantsForUser only.
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

// effectivePermissionsField is the MEG-015 §09 Permissions query
// "effective permission inspection". It calls
// app.Service.GetEffectivePermissions only.
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
