package graphql

import (
	"time"

	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// formatTime renders t as RFC3339, or "" for the zero time. Every
// timestamp field in this schema is exposed as a String rather than the
// DateTime scalar, so a nil *time.Time (ValidatedAt, ActivatedAt, ...)
// serializes as a plain empty string instead of depending on a scalar's
// nil-pointer handling.
func formatTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(time.RFC3339)
}

// formatTimePtr renders *t as RFC3339, or "" if t is nil.
func formatTimePtr(t *time.Time) string {
	if t == nil {
		return ""
	}
	return formatTime(*t)
}

// permissionsToStrings converts a []domain.Permission to []string for the
// GraphQL list-of-String fields below (graphql-go's default resolver does
// not know how to serialize a named string slice type on its own).
func permissionsToStrings(permissions []domain.Permission) []string {
	out := make([]string, len(permissions))
	for i, p := range permissions {
		out[i] = string(p)
	}
	return out
}

// userType is the GraphQL projection of domain.User (MEG-015 §09 — Users).
var userType = graphql.NewObject(graphql.ObjectConfig{
	Name: "User",
	Fields: graphql.Fields{
		"id":          &graphql.Field{Type: graphql.String},
		"username":    &graphql.Field{Type: graphql.String},
		"email":       &graphql.Field{Type: graphql.String},
		"displayName": &graphql.Field{Type: graphql.String},
		"status":      &graphql.Field{Type: graphql.String},
		"createdAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(domain.User).CreatedAt), nil
		}},
		"updatedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(domain.User).UpdatedAt), nil
		}},
	},
})

// sessionType is the GraphQL projection of domain.Session, used for the
// signIn payload (MEG-015 §09 — Auth).
var sessionType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Session",
	Fields: graphql.Fields{
		"id":       &graphql.Field{Type: graphql.String},
		"userId":   &graphql.Field{Type: graphql.String},
		"deviceId": &graphql.Field{Type: graphql.String},
		"issuedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(domain.Session).IssuedAt), nil
		}},
		"lastSeenAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(domain.Session).LastSeenAt), nil
		}},
		"expiresAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(domain.Session).ExpiresAt), nil
		}},
		"authStrength": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return string(p.Source.(domain.Session).AuthStrength), nil
		}},
		"capabilities": &graphql.Field{Type: graphql.NewList(graphql.String), Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return permissionsToStrings(p.Source.(domain.Session).Capabilities), nil
		}},
		"revokedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTimePtr(p.Source.(domain.Session).RevokedAt), nil
		}},
	},
})

// roleType is the GraphQL projection of domain.Role (MEG-015 §09 —
// Permissions: "roles").
var roleType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Role",
	Fields: graphql.Fields{
		"id":   &graphql.Field{Type: graphql.String},
		"name": &graphql.Field{Type: graphql.String},
		"permissions": &graphql.Field{Type: graphql.NewList(graphql.String), Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return permissionsToStrings(p.Source.(domain.Role).Permissions), nil
		}},
	},
})

// grantType is the GraphQL projection of domain.Grant (MEG-015 §09 —
// Permissions: "grants").
var grantType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Grant",
	Fields: graphql.Fields{
		"userId": &graphql.Field{Type: graphql.String},
		"roleId": &graphql.Field{Type: graphql.String},
	},
})

// configVersionType is the GraphQL projection of domain.ConfigVersion
// (MEG-015 §09 — Configuration).
var configVersionType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ConfigVersion",
	Fields: graphql.Fields{
		"id":     &graphql.Field{Type: graphql.String},
		"status": &graphql.Field{Type: graphql.String},
		"payload": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return string(p.Source.(domain.ConfigVersion).Payload), nil
		}},
		"createdAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTime(p.Source.(domain.ConfigVersion).CreatedAt), nil
		}},
		"validatedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTimePtr(p.Source.(domain.ConfigVersion).ValidatedAt), nil
		}},
		"validationDetail": &graphql.Field{Type: graphql.String},
		"activatedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTimePtr(p.Source.(domain.ConfigVersion).ActivatedAt), nil
		}},
		"rejectedAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTimePtr(p.Source.(domain.ConfigVersion).RejectedAt), nil
		}},
		"supersededAt": &graphql.Field{Type: graphql.String, Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			return formatTimePtr(p.Source.(domain.ConfigVersion).SupersededAt), nil
		}},
	},
})

// activateConfigVersionPayloadType wraps ActivateConfigVersionResult:
// unlike the other Configuration mutations, activation can legitimately
// not take effect yet (a non-Hot reload class), so the payload surfaces
// that outcome explicitly rather than only the resulting version.
var activateConfigVersionPayloadType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ActivateConfigVersionPayload",
	Fields: graphql.Fields{
		"version":     &graphql.Field{Type: configVersionType},
		"activated":   &graphql.Field{Type: graphql.Boolean},
		"reloadClass": &graphql.Field{Type: graphql.String},
	},
})
