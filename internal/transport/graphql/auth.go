package graphql

import (
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// signInPayloadType wraps AuthenticateLocalUserResult.
var signInPayloadType = graphql.NewObject(graphql.ObjectConfig{
	Name: "SignInPayload",
	Fields: graphql.Fields{
		"session": &graphql.Field{Type: sessionType},
	},
})

// signOutPayloadType wraps RevokeSessionResult.
var signOutPayloadType = graphql.NewObject(graphql.ObjectConfig{
	Name: "SignOutPayload",
	Fields: graphql.Fields{
		"sessionId": &graphql.Field{Type: graphql.String},
	},
})

// signInField is the MEG-015 §09 Auth mutation "local sign-in". The
// resolver's entire body is a call into app.Service.AuthenticateLocalUser
// — it never touches a store or a database connection directly (MEG-015
// §09 — GraphQL Role).
func signInField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: signInPayloadType,
		Args: graphql.FieldConfigArgument{
			"username": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"password": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"deviceId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.AuthenticateLocalUser(p.Context, app.AuthenticateLocalUserCommand{
				Username: p.Args["username"].(string),
				Password: p.Args["password"].(string),
				DeviceID: domain.DeviceID(p.Args["deviceId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"session": result.Session}, nil
		},
	}
}

// signOutField is the MEG-015 §09 Auth mutation "sign-out", implemented as
// a server-side session revocation (MEG-015 §07): it calls
// app.Service.RevokeSession only.
func signOutField(svc *app.Service) *graphql.Field {
	return &graphql.Field{
		Type: signOutPayloadType,
		Args: graphql.FieldConfigArgument{
			"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
			"targetSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			result, err := svc.RevokeSession(p.Context, app.RevokeSessionCommand{
				CallerSessionID: domain.SessionID(p.Args["callerSessionId"].(string)),
				TargetSessionID: domain.SessionID(p.Args["targetSessionId"].(string)),
			})
			if err != nil {
				return nil, err
			}
			return map[string]interface{}{"sessionId": string(result.SessionID)}, nil
		},
	}
}

// notImplementedField builds a field whose resolver always fails with an
// Unavailable Platform error explaining why, rather than a value —
// GraphQL's Auth surface names "session refresh" and "remote sign-in
// challenge status" (MEG-015 §09) as required, but no application service
// backs either yet: sessions.Manager has Issue/Validate/Revoke only (no
// refresh), and no device-pairing/challenge flow exists at all. Inventing
// that behavior in a resolver would violate the hard rule that resolvers
// only call application services — so the gap is flagged instead of
// papered over.
func notImplementedField(fieldType graphql.Output, args graphql.FieldConfigArgument, gap string) *graphql.Field {
	return &graphql.Field{
		Type: fieldType,
		Args: args,
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return nil, contracts.NewError(contracts.Unavailable, "not implemented: "+gap)
		},
	}
}

// sessionRefreshField stubs MEG-015 §09's "session refresh" — see
// notImplementedField.
func sessionRefreshField() *graphql.Field {
	return notImplementedField(sessionType, graphql.FieldConfigArgument{
		"sessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, "no application service exists yet to refresh a session's lifetime (sessions.Manager has Issue/Validate/Revoke only)")
}

// remoteSignInChallengeStatusField stubs MEG-015 §09's "remote sign-in
// challenge status" — see notImplementedField.
func remoteSignInChallengeStatusField() *graphql.Field {
	return notImplementedField(graphql.String, graphql.FieldConfigArgument{
		"challengeId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, "no remote/device sign-in challenge flow exists yet")
}
