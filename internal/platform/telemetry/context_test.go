// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry_test

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// TestFromAnUnseededContextIsSilentAndSafe is the property the whole ambient
// design rests on: a call site writes telemetry.From(ctx).Info(…) with no nil
// check, so the unseeded case must be a working no-op rather than a panic.
// A library path, a test, or a goroutine started from context.Background()
// must degrade quietly.
func TestFromAnUnseededContextIsSilentAndSafe(t *testing.T) {
	telemetry.From(context.Background()).Info("nobody is listening",
		telemetry.String("k", "v"))
	telemetry.From(context.Background()).For("x").With(telemetry.Int("n", 1)).Error("still fine")
	//nolint:staticcheck // deliberately passing a nil context: From must survive it.
	telemetry.From(nil).Info("even this")
}

func TestIntoAndFromRoundTrip(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelDebug)

	ctx := telemetry.Into(context.Background(), logger)
	telemetry.From(ctx).Info("hello")

	if !strings.Contains(buf.String(), "hello") {
		t.Fatalf("expected the seeded logger to receive the record, got: %s", buf.String())
	}
}

// TestWithAccumulatesDownTheContextChain is the ergonomic claim: an edge binds
// scope once, and a call site several layers down inherits all of it without
// naming any of it.
func TestWithAccumulatesDownTheContextChain(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelDebug)

	ctx := telemetry.Into(context.Background(), logger)
	ctx = telemetry.For(ctx, "session")
	ctx = telemetry.With(ctx, telemetry.String("lane", "subscribe"))
	ctx = telemetry.With(ctx, telemetry.Identifier("session", "abc"))

	// A function this deep names one field and inherits three.
	telemetry.From(ctx).Info("stream open", telemetry.Int("resume", 7))

	line := parseLogLine(t, buf.Bytes())
	if line["component"] != "session" {
		t.Fatalf("component = %v, want session", line["component"])
	}
	fields, _ := line["fields"].(map[string]interface{})
	for _, key := range []string{"lane", "session", "resume"} {
		if _, ok := fields[key]; !ok {
			t.Fatalf("expected inherited field %q in %v", key, fields)
		}
	}
}

// TestIntoIgnoresANilLogger guards against a caller blanking out a logger an
// outer scope already established.
func TestIntoIgnoresANilLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := telemetry.New(telemetry.NewJSONSink(&buf), telemetry.Resource{}, telemetry.LevelDebug)

	ctx := telemetry.Into(context.Background(), logger)
	ctx = telemetry.Into(ctx, nil)

	telemetry.From(ctx).Info("still reaches the sink")
	if !strings.Contains(buf.String(), "still reaches the sink") {
		t.Fatalf("a nil logger must not displace the established one, got: %s", buf.String())
	}
}
