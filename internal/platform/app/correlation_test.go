// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The event envelope has carried CorrelationID and CausationID since it was
// written, with a comment saying they were "empty until request-scoped
// propagation exists". These tests are that propagation arriving (platform#32):
// the correlation id is the trace id, so an outbox row and the log lines
// around it share one key with no second identifier invented to get there.

func TestCommandStampsTheTraceOntoItsOutboxEvent(t *testing.T) {
	cap := &recordingCapability{id: "stremio"}
	svc, db, _, session := importFixture(t, cap)
	caller := v1.Caller{Session: string(session)}

	// A request arriving at an edge — this is what StartRequest produces from
	// an inbound traceparent, or mints when there is none.
	tc := telemetry.NewTraceContext()
	ctx := telemetry.TraceInto(context.Background(), tc)

	if _, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
		Caller: caller, ModuleID: "stremio", Settings: []byte(`{"addons":[]}`),
	}); err != nil {
		t.Fatalf("ConfigureModule: %v", err)
	}

	var found bool
	for _, e := range db.outbox {
		if e.Type != "module.configured" {
			continue
		}
		found = true
		if e.CorrelationID != tc.TraceIDString() {
			t.Fatalf("CorrelationID = %q, want the trace id %q", e.CorrelationID, tc.TraceIDString())
		}
		if e.CausationID != tc.SpanIDString() {
			t.Fatalf("CausationID = %q, want the span id %q", e.CausationID, tc.SpanIDString())
		}
	}
	if !found {
		t.Fatalf("no module.configured event was committed: %v", db.outboxTypes())
	}
}

// TestCommandWithoutATraceLeavesTheIDsEmpty is the deliberate non-behaviour:
// background work that has no request behind it must not manufacture a trace.
// An empty correlation id honestly says "nothing requested this"; a minted one
// would imply a caller that never existed.
func TestCommandWithoutATraceLeavesTheIDsEmpty(t *testing.T) {
	cap := &recordingCapability{id: "stremio"}
	svc, db, _, session := importFixture(t, cap)
	caller := v1.Caller{Session: string(session)}

	if _, err := svc.ConfigureModule(context.Background(), app.ConfigureModuleCommand{
		Caller: caller, ModuleID: "stremio", Settings: []byte(`{"addons":[]}`),
	}); err != nil {
		t.Fatalf("ConfigureModule: %v", err)
	}

	for _, e := range db.outbox {
		if e.Type == "module.configured" && (e.CorrelationID != "" || e.CausationID != "") {
			t.Fatalf("expected empty ids without a trace, got %q/%q", e.CorrelationID, e.CausationID)
		}
	}
}

// TestOneTraceJoinsEveryEventOfARequest is the property that makes a
// multi-repository failure tractable: two events committed by one request carry
// the same correlation id, so "what else happened because of this?" is a query
// rather than a reconstruction from timestamps.
func TestOneTraceJoinsEveryEventOfARequest(t *testing.T) {
	cap := &recordingCapability{id: "stremio"}
	svc, db, _, session := importFixture(t, cap)
	caller := v1.Caller{Session: string(session)}

	tc := telemetry.NewTraceContext()
	ctx := telemetry.TraceInto(context.Background(), tc)

	for _, settings := range []string{`{"addons":[]}`, `{"addons":["https://example.test/manifest.json"]}`} {
		if _, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
			Caller: caller, ModuleID: "stremio", Settings: []byte(settings),
		}); err != nil {
			t.Fatalf("ConfigureModule: %v", err)
		}
	}

	seen := 0
	for _, e := range db.outbox {
		if e.CorrelationID == tc.TraceIDString() {
			seen++
		}
	}
	if seen < 2 {
		t.Fatalf("expected both events joined by one correlation id, got %d", seen)
	}
}
