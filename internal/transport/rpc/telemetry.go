// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package rpc

import (
	"context"
	"time"

	"connectrpc.com/connect"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// TelemetryInterceptor is the edge seam for the first-party client transport
// (ADR 0055, seam 1) — and the most important one, because this is where a
// user's action enters the Platform. It continues the trace the Shell started
// or begins one, binds a logger to it, and records the outcome of each call.
//
// component names the surface in every record it produces ("auth", "session"),
// so a record says which service answered rather than only that Connect did.
// Since ADR 0061 those two are the whole client API, and both mount this.
//
// Both call shapes are covered, and they are covered differently on purpose
// (ADR 0041 gives them different lifetimes):
//
//   - **Unary calls** — SignIn/SignOut, and the Attach/Navigate/Invoke/
//     SubmitInput intents — get one record each, with the duration and whether
//     they failed. These are short.
//   - **Subscribe** — one long-lived server stream per session — gets an open
//     and a close record with the elapsed time between them. A record per
//     pushed message on a stream that lives for hours would drown everything
//     else; the messages are already observable where they are enqueued.
//
// Seeding the context here rather than in each method is what lets every
// handler, every application service beneath it, and every module it invokes
// reach telemetry without any of them taking a parameter for it.
func TelemetryInterceptor(component string) connect.Interceptor {
	return &telemetryInterceptor{component: component}
}

type telemetryInterceptor struct{ component string }

// WrapUnary handles every unary call on either service.
func (i *telemetryInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		ctx = i.start(ctx, req.Header().Get(telemetry.TraceparentHeader))
		lg := telemetry.From(ctx)

		started := time.Now()
		res, err := next(ctx, req)
		fields := []telemetry.Field{
			telemetry.String("procedure", req.Spec().Procedure),
			telemetry.Duration("elapsed", time.Since(started).Round(time.Millisecond)),
		}
		if err != nil {
			// The Connect code is a closed vocabulary and safe verbatim; the
			// error text is not necessarily, but it originates in the Platform
			// rather than a user, so Err's standing caveat applies and no more.
			lg.Error("call failed", append(fields,
				telemetry.String("code", connect.CodeOf(err).String()),
				telemetry.Err(err))...)
		} else {
			lg.Info("call", fields...)
		}
		return res, err
	}
}

// WrapStreamingHandler handles the session push lane — the Subscribe stream.
func (i *telemetryInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		ctx = i.start(ctx, conn.RequestHeader().Get(telemetry.TraceparentHeader))
		lg := telemetry.From(ctx).With(telemetry.String("procedure", conn.Spec().Procedure))

		started := time.Now()
		lg.Info("stream opened")
		err := next(ctx, conn)
		elapsed := telemetry.Duration("elapsed", time.Since(started).Round(time.Millisecond))
		if err != nil {
			lg.Error("stream failed", elapsed,
				telemetry.String("code", connect.CodeOf(err).String()),
				telemetry.Err(err))
		} else {
			lg.Info("stream ended", elapsed)
		}
		return err
	}
}

// WrapStreamingClient is required by the interface. The Platform is not a
// Connect client of anything today, so this is a pass-through rather than a
// half-instrumented path that looks like it works.
func (i *telemetryInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

// start seeds the trace and the component for one call.
func (i *telemetryInterceptor) start(ctx context.Context, traceparent string) context.Context {
	return telemetry.For(telemetry.StartRequest(ctx, traceparent), i.component)
}
