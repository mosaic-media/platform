package graphql

import "github.com/graphql-go/graphql"

// jobType is a schema-shape placeholder matching the columns migration
// 0006_jobs.sql already created (id, kind, status, ...) — no domain type,
// contract, or application service exists on top of that table yet ("table
// only this slice; the job runner is not part of the Platform foundation
// build sequence yet", per that migration's own comment). Every Jobs
// resolver below is a flagged stub, not a real projection of it.
var jobType = graphql.NewObject(graphql.ObjectConfig{
	Name: "Job",
	Fields: graphql.Fields{
		"id":     &graphql.Field{Type: graphql.String},
		"kind":   &graphql.Field{Type: graphql.String},
		"status": &graphql.Field{Type: graphql.String},
	},
})

// jobLogType is a schema-shape placeholder matching job_logs — see jobType.
var jobLogType = graphql.NewObject(graphql.ObjectConfig{
	Name: "JobLog",
	Fields: graphql.Fields{
		"loggedAt": &graphql.Field{Type: graphql.String},
		"level":    &graphql.Field{Type: graphql.String},
		"message":  &graphql.Field{Type: graphql.String},
	},
})

// jobsGap is the flagged reason every Jobs field below fails: there is no
// Jobs application service to call, and MEG-015 §09's GraphQL Role forbids
// resolvers from reaching around one — e.g. by querying the `jobs` table
// directly. Building a real Jobs system is explicitly out of scope for
// this slice (MEG-015 §12 — GraphQL); the schema shape exists so the
// surface §09 requires is visible, but every resolver is a stub.
const jobsGap = "jobs infrastructure is not implemented yet (migration 0006_jobs.sql created tables only; no JobStore contract or application service exists)"

// jobsField stubs MEG-015 §09's "job list" — see jobsGap.
func jobsField() *graphql.Field {
	return notImplementedField(graphql.NewList(jobType), graphql.FieldConfigArgument{
		"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, jobsGap)
}

// jobField stubs MEG-015 §09's "job detail" — see jobsGap.
func jobField() *graphql.Field {
	return notImplementedField(jobType, graphql.FieldConfigArgument{
		"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		"id":              &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, jobsGap)
}

// jobLogsField stubs MEG-015 §09's "job logs" — see jobsGap.
func jobLogsField() *graphql.Field {
	return notImplementedField(graphql.NewList(jobLogType), graphql.FieldConfigArgument{
		"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		"jobId":           &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, jobsGap)
}

// retryJobField stubs MEG-015 §09's "retry command" — see jobsGap.
func retryJobField() *graphql.Field {
	return notImplementedField(jobType, graphql.FieldConfigArgument{
		"callerSessionId": &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
		"jobId":           &graphql.ArgumentConfig{Type: graphql.NewNonNull(graphql.String)},
	}, jobsGap)
}
