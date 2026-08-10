// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package telemetry

import (
	"fmt"
	"net/http"
	"time"
)

// TraceIDHeader is set on every HTTP response so a client — or a person
// reading a browser's network tab — can see the trace their request belongs
// to. A bug report that arrives carrying this value is a lookup rather than an
// investigation (platform#32), which is most of why it is worth a header.
//
// It is deliberately a Mosaic-specific name rather than the W3C `traceresponse`
// draft: that draft is not settled, and this is for a human and a first-party
// client, not for interoperation.
const TraceIDHeader = "Mosaic-Trace-Id"

// HTTPMiddleware is the edge seam for every plain HTTP surface — the artwork
// proxy, the playback origin and the Supervisor handoff
// (platform#33, seams 2 and 3). It continues an inbound trace or starts one,
// binds a logger to it, and echoes the trace id back on the response.
//
// It lives here rather than in a transport package because all four surfaces
// need the identical thing, and a second copy of the seam is a second copy to
// get subtly wrong. It brings only net/http with it, which costs the package
// nothing it did not already have from the standard library.
func HTTPMiddleware(component string, next http.Handler) http.Handler {
	return instrument(component, next, nil)
}

// HTTPMuxMiddleware is HTTPMiddleware for a whole *http.ServeMux.
//
// It exists because wrapping a mux from the outside cannot see which route was
// matched: net/http populates r.Pattern during mux dispatch, which is after
// this middleware has already run, so every record would name the surface
// ("handoff") rather than the route ("/readyz"). Resolving the pattern up front
// costs one extra match and is what makes the operational surface — the one an
// operator reads first when something will not start — actually legible.
//
// Where the middleware is registered per-route instead (apiMux does this, one
// Handle call per surface), r.Pattern is already set by the time it runs and
// plain HTTPMiddleware is enough.
func HTTPMuxMiddleware(component string, mux *http.ServeMux) http.Handler {
	return instrument(component, mux, func(r *http.Request) string {
		_, pattern := mux.Handler(r)
		return pattern
	})
}

// instrument is the shared body. resolveRoute may be nil, in which case the
// route comes from r.Pattern (set by an enclosing mux) or the component name.
func instrument(component string, next http.Handler, resolveRoute func(*http.Request) string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := StartRequest(r.Context(), r.Header.Get(TraceparentHeader))
		ctx = For(ctx, component)
		if tc, ok := TraceFrom(ctx); ok {
			// Set before next.ServeHTTP: a handler that writes its status
			// immediately would otherwise flush headers before this ran.
			w.Header().Set(TraceIDHeader, tc.TraceIDString())
		}

		// The route pattern, never r.URL.Path. A playback path carries a sealed
		// ticket and an artwork path a signed URL — both credential-bearing, and
		// both would be written verbatim by the obvious version of this line.
		// The pattern is what was matched, which is what anyone reading this
		// actually wants, and it carries nothing.
		//
		// Resolved before the handler runs so it can name the span, which has
		// to be named at Start rather than at End.
		route := r.Pattern
		if route == "" && resolveRoute != nil {
			route = resolveRoute(r)
		}
		if route == "" {
			route = component
		}

		ctx, span := Start(ctx, r.Method+" "+route,
			String("http.method", r.Method),
			String("http.route", route))
		defer span.End()

		rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
		started := time.Now()
		next.ServeHTTP(rec, r.WithContext(ctx))

		span.SetAttributes(Int("http.status", rec.status))
		if rec.status >= 500 {
			// Only 5xx marks the span failed. A 404 or a 401 is the surface
			// working correctly, and colouring those as errors would make the
			// failure view useless on the two paths that produce them most.
			span.Fail("", errStatus(rec.status))
		}

		From(ctx).Info("http request",
			String("method", r.Method),
			String("route", route),
			Int("status", rec.status),
			Duration("elapsed", time.Since(started).Round(time.Millisecond)))
	})
}

// statusRecorder captures the status code for the record above. It forwards
// everything else untouched — these surfaces stream (the playback origin
// relays byte ranges, artwork proxies images), so losing Flush or the
// ResponseController escape hatch would turn observation into a regression.
type statusRecorder struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (s *statusRecorder) WriteHeader(code int) {
	if !s.wrote {
		s.status, s.wrote = code, true
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(b []byte) (int, error) {
	// A handler that writes without calling WriteHeader has implicitly sent 200.
	s.wrote = true
	return s.ResponseWriter.Write(b)
}

// Unwrap lets http.NewResponseController reach the real writer, so a handler
// that needs Flush, Hijack or a deadline still can.
func (s *statusRecorder) Unwrap() http.ResponseWriter { return s.ResponseWriter }

// Flush forwards to the underlying writer when it supports flushing, so a
// streaming response is not buffered by this wrapper.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

// errStatus renders a 5xx as an error for a span's failure record. The handler
// already wrote whatever it wrote; this exists only so the span carries some
// reason rather than an empty one.
func errStatus(status int) error {
	return fmt.Errorf("http status %d", status)
}
