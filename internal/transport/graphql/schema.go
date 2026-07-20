// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package graphql is the GraphQL command and query projection transport
// (MEG-015 §09, §12 — GraphQL command and query surface). It is a
// transport and projection surface only: every resolver in this package
// calls an application command or query service (internal/platform/app)
// and nothing else. Resolvers must never open a database connection or
// import internal/modules/postgres directly (MEG-015 §09 — GraphQL Role);
// boundary_test.go statically enforces this.
//
// Covers the areas MEG-015 §09's First GraphQL Surface table requires:
// Auth, Users, Permissions and Configuration are real, backed by
// application services from earlier slices. Jobs and Health are schema
// stubs — no Jobs or cross-component Diagnostics application service
// exists yet, so their resolvers return a flagged "not implemented" error
// rather than inventing one (see jobs.go, health.go). Diagnostics (support
// bundle) and product/media queries are out of scope for this slice
// entirely (the former belongs to the Diagnostics and health slice; the
// latter MEG-015 §09 explicitly defers to "the relevant Module").
package graphql

import (
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/transport/screens"
)

// NewSchema builds the executable GraphQL schema for svc. Every resolver
// closes over svc and calls exactly one of its command/query methods. artwork
// rewrites a remote image URL to a Platform-proxied one for emitted screens
// (ADR 0030); pass nil to leave URLs unchanged.
func NewSchema(svc *app.Service, artwork func(string) string) (graphql.Schema, error) {
	// The SDUI emit-side (ADR 0029) projects application queries into screens;
	// the screen query below serves them.
	screenSvc := screens.NewService(svc, artwork)

	query := graphql.NewObject(graphql.ObjectConfig{
		Name: "Query",
		Fields: graphql.Fields{
			// Users.
			"users": usersField(svc),
			"user":  userField(svc),
			// Permissions.
			"rolesForUser":         rolesForUserField(svc),
			"grantsForUser":        grantsForUserField(svc),
			"effectivePermissions": effectivePermissionsField(svc),
			// Configuration.
			"activeConfigVersion": activeConfigVersionField(svc),
			"configVersion":       configVersionField(svc),
			// Modules.
			"moduleSettings": moduleSettingsField(svc),
			// Content.
			"searchContent":       searchContentField(svc),
			"contentNode":         contentNodeField(svc),
			"contentByExternalId": contentByExternalIDField(svc),
			// Content discovery through modules (virtual plane — ADR 0028).
			"searchAvailableContent": searchAvailableContentField(svc),
			"moduleCatalogs":         moduleCatalogsField(svc),
			"catalogItems":           catalogItemsField(svc),
			// Server-emitted SDUI screens (ADR 0029).
			"screen": screenField(screenSvc),
			// Auth.
			"remoteSignInChallengeStatus": remoteSignInChallengeStatusField(),
			// Jobs (stub — see jobs.go).
			"jobs":    jobsField(),
			"job":     jobField(),
			"jobLogs": jobLogsField(),
			// Health (stub — see health.go).
			"componentHealth": componentHealthField(),
		},
	})

	mutation := graphql.NewObject(graphql.ObjectConfig{
		Name: "Mutation",
		Fields: graphql.Fields{
			// Auth.
			"signIn":         signInField(svc),
			"signOut":        signOutField(svc),
			"refreshSession": sessionRefreshField(),
			// Users.
			"setUserStatus": setUserStatusField(svc),
			// Permissions.
			"createRole": createRoleField(svc),
			"grantRole":  grantRoleField(svc),
			// Configuration.
			"draftConfigVersion":    draftConfigVersionField(svc),
			"validateConfigVersion": validateConfigVersionField(svc),
			"activateConfigVersion": activateConfigVersionField(svc),
			// Content.
			"addContentWork":        addContentWorkField(svc),
			"addContentChild":       addContentChildField(svc),
			"attachContentPart":     attachContentPartField(svc),
			"relateContent":         relateContentField(svc),
			"bindContentSource":     bindContentSourceField(svc),
			"resolveContentBinding": resolveContentBindingField(svc),
			"importContent":         importContentField(svc),
			"configureModule":       configureModuleField(svc),
			// Jobs (stub — see jobs.go).
			"retryJob": retryJobField(),
		},
	})

	return graphql.NewSchema(graphql.SchemaConfig{
		Query:    query,
		Mutation: mutation,
	})
}
