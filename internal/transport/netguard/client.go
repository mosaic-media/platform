// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package netguard

import (
	"net/http"
	"time"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
)

// ModuleClient builds the HTTP client the composition root hands to every
// module (platform#33, seam 9).
//
// It closes two holes at once, and the second is the more urgent of the two.
//
// **Telemetry.** A module's outbound call is usually where the time goes — a
// Stremio aggregator fanning out to scrapers takes hundreds of milliseconds to
// seconds — and until now it was the part of a trace that simply stopped. Each
// request gets a span, and the trace context travels on the wire, so a
// cooperating upstream could continue the trace rather than starting its own.
//
// **SSRF.** `netguard` exists because any handler fetching a URL on a client's
// behalf opens a hole unless something stops it reaching the host's own
// network. The artwork proxy and the playback origin use the dial guard — and
// modules, which fetch third-party URLs a *user* supplied through module
// settings, did not: each one built its own `http.Client` because the
// composition root passed nil. That is the same class of hole the guard was
// written for, reached by a different route, and it is closed here by giving
// every module a client that cannot bypass it.
func ModuleClient() *http.Client {
	return &http.Client{
		// Generous, because an aggregator legitimately takes seconds. Bounded,
		// because a module must not be able to pin a request open forever.
		Timeout: 60 * time.Second,
		Transport: &tracedTransport{
			next: &http.Transport{
				DialContext:           DialContext,
				MaxIdleConns:          64,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: time.Second,
			},
		},
	}
}

// tracedTransport spans one outbound request and propagates trace context.
type tracedTransport struct{ next http.RoundTripper }

func (t *tracedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Host and method only — never the full URL. A module's outbound URL can
	// carry an API key in its query string (a debrid token, an addon secret),
	// and recording it would put a credential in the telemetry store through
	// the one path that most often holds one.
	ctx, span := telemetry.Start(req.Context(), "http "+req.Method+" "+req.URL.Host,
		telemetry.String("http.method", req.Method),
		telemetry.String("http.host", req.URL.Host),
		telemetry.String("net.direction", "outbound"),
	)
	defer span.End()

	// Propagate onward. Nothing Mosaic talks to reads this today, but it costs
	// one header and it means a self-hosted upstream that does understand it
	// joins the same trace instead of starting a new one.
	req = req.Clone(ctx)
	if tc, ok := telemetry.TraceFrom(ctx); ok && tc.Valid() {
		req.Header.Set(telemetry.TraceparentHeader, tc.Traceparent())
	}

	res, err := t.next.RoundTrip(req)
	if err != nil {
		// Unavailable: an outbound call that could not complete is the
		// Platform being unable to reach something, which is what that
		// category means.
		span.Fail("unavailable", err)
		return nil, err
	}
	span.SetAttributes(telemetry.Int("http.status", res.StatusCode))
	return res, nil
}
