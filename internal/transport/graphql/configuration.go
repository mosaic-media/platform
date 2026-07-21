// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package graphql

import (
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// draftConfigVersionField is the Configuration mutation "config draft". It
// calls app.Service.DraftConfigVersion only.
func draftConfigVersionField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: configVersionType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"payload":         &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.DraftConfigVersion(p.Context, app.DraftConfigVersionCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				Payload:         []byte(p.Args["payload"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Version, nil
		},
	}
}

// validateConfigVersionField is the Configuration mutation "config
// validation". It calls app.Service.ValidateConfigVersion only.
func validateConfigVersionField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: configVersionType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"configVersionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ValidateConfigVersion(p.Context, app.ValidateConfigVersionCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				ConfigVersionID: domain.ConfigVersionID(p.Args["configVersionId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Version, nil
		},
	}
}

// activateConfigVersionField is the Configuration mutation "config
// activation". It calls app.Service.ActivateConfigVersion only. The
// payload reports Activated/ReloadClass explicitly, since a non-Hot-classed
// change is correctly deferred rather than applied.
func activateConfigVersionField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: activateConfigVersionPayloadType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"configVersionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.ActivateConfigVersion(p.Context, app.ActivateConfigVersionCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				ConfigVersionID: domain.ConfigVersionID(p.Args["configVersionId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{
				"version":     result.Version,
				"activated":   result.Activated,
				"reloadClass": string(result.ReloadClass),
			}, nil
		},
	}
}

// activeConfigVersionField is the Configuration query "active version". It
// calls app.Service.GetActiveConfigVersion only.
func activeConfigVersionField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: configVersionType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetActiveConfigVersion(p.Context, app.GetActiveConfigVersionQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Version, nil
		},
	}
}

// configVersionField reads a single configuration version by ID, so a
// caller can check the outcome of a prior draft/validate/activate
// mutation. It calls app.Service.GetConfigVersion only.
func configVersionField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: configVersionType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"id":              &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.GetConfigVersion(p.Context, app.GetConfigVersionQuery{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				ConfigVersionID: domain.ConfigVersionID(p.Args["id"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return result.Version, nil
		},
	}
}
