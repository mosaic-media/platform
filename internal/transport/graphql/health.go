package graphql

import "github.com/graphql-go/graphql"

// componentHealthType is a schema-shape placeholder for domain.HealthStatus
// (Component/State/Detail/CheckedAt already exist, and a real
// contracts.HealthProbe already exists per-adapter — e.g. Postgres's — but
// there is no cross-component aggregation, no lifecycle-state/degraded-
// reason/last-failure-category/dependency-health/redaction-class fields
// MEG-015 §09's Diagnostics Model lists, and no application service
// exposing any of it yet. That is explicitly the Diagnostics and health
// slice's job (MEG-015 §12), not this one.
var componentHealthType = graphql.NewObject(graphql.ObjectConfig{
	Name: "ComponentHealth",
	Fields: graphql.Fields{
		"component": &graphql.Field{Type: graphql.String},
		"state":     &graphql.Field{Type: graphql.String},
		"detail":    &graphql.Field{Type: graphql.String},
	},
})

// healthGap is the flagged reason the Health field below fails — see
// componentHealthType.
const healthGap = "component health aggregation is not implemented yet (contracts.HealthProbe exists per-adapter, e.g. Postgres's, but no cross-component application service exists — that is the Diagnostics and health slice's job)"

// componentHealthField stubs MEG-015 §09's "component health and degraded
// component detail" — see healthGap.
func componentHealthField() *graphql.Field {
	return notImplementedField(graphql.NewList(componentHealthType), graphql.FieldConfigArgument{
		"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, healthGap)
}
