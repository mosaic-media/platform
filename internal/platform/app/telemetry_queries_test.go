// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Reading stored telemetry (ADR 0058). The gate matters more than the query:
// telemetry records what every user did, so telemetry.read is an escalation an
// administrator must be granted individually (ADR 0069). The superuser holds
// it; see roles_test.go for the tier boundary itself.

// TestTelemetryReadIsDeniedWithoutTheGrant is the property the whole
// expert-mode surface rests on. adminRole() is the Administrator tier — a wide
// set of operational actions — and still must not reach telemetry.
func TestTelemetryReadIsDeniedWithoutTheGrant(t *testing.T) {
	ctx := context.Background()
	svc, _, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	_, err := svc.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{Caller: caller})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("QueryTelemetryLogs category = %v, want permission_denied", contracts.CategoryOf(err))
	}

	_, err = svc.GetTrace(ctx, app.GetTraceQuery{Caller: caller, TraceID: "abc"})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("GetTrace category = %v, want permission_denied", contracts.CategoryOf(err))
	}

	_, err = svc.ListTraces(ctx, app.ListTracesQuery{Caller: caller})
	if contracts.CategoryOf(err) != contracts.PermissionDenied {
		t.Fatalf("ListTraces category = %v, want permission_denied", contracts.CategoryOf(err))
	}
}

func TestTelemetryReadRequiresAnAuthenticatedCaller(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := importFixture(t)

	_, err := svc.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{
		Caller: v1.Caller{Session: "nope"},
	})
	if contracts.CategoryOf(err) != contracts.Unauthenticated {
		t.Fatalf("category = %v, want unauthenticated", contracts.CategoryOf(err))
	}
}

// TestTelemetryReadWorksWithTheGrant confirms the gate opens, and that the
// filter reaches the store rather than being dropped on the way.
func TestTelemetryReadWorksWithTheGrant(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}
	db.grantPermission("u-1", domain.Permission(app.ActionTelemetryRead))

	res, err := svc.QueryTelemetryLogs(ctx, app.QueryTelemetryLogsQuery{
		Caller: caller,
		Filter: domain.TelemetryLogFilter{Component: "session", MinLevel: "warn"},
	})
	if err != nil {
		t.Fatalf("QueryTelemetryLogs: %v", err)
	}
	if len(res.Records) != 1 || res.Records[0].Component != "session" {
		t.Fatalf("filter did not reach the store: %+v", res.Records)
	}
}

// TestGetTraceReportsNotFoundForAnUnknownTrace — the usual way to reach this is
// pasting an id from a bug report, and "no such trace" and "that trace did
// nothing" are different answers.
func TestGetTraceReportsNotFoundForAnUnknownTrace(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}
	db.grantPermission("u-1", domain.Permission(app.ActionTelemetryRead))

	_, err := svc.GetTrace(ctx, app.GetTraceQuery{Caller: caller, TraceID: "no-such-trace"})
	if contracts.CategoryOf(err) != contracts.NotFound {
		t.Fatalf("category = %v, want not_found", contracts.CategoryOf(err))
	}
}

// TestCallerCanGatesTheAffordance is the visibility rule: a normal user must
// not even see the expert-mode toggle, so the emit-side asks this before
// drawing it. It answers about authority without granting any, and fails
// closed.
func TestCallerCanGatesTheAffordance(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	if svc.CallerCan(ctx, caller, app.ActionTelemetryRead, "telemetry") {
		t.Fatal("a user without the grant must not be offered the affordance")
	}

	db.grantPermission("u-1", domain.Permission(app.ActionTelemetryRead))
	if !svc.CallerCan(ctx, caller, app.ActionTelemetryRead, "telemetry") {
		t.Fatal("a user with the grant should be offered the affordance")
	}

	// An unauthenticated caller is not an error and not a yes.
	if svc.CallerCan(ctx, v1.Caller{Session: "nope"}, app.ActionTelemetryRead, "telemetry") {
		t.Fatal("an unauthenticated caller must fail closed")
	}
}
