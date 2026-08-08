// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package clientaddr resolves the address a request should be attributed to,
// which is not always the address it arrived from.
//
// Behind the Supervisor's front door every request arrives from the Supervisor
// (ADR 0120), so the connection's peer identifies the proxy rather than the
// caller — and over a Unix socket there is no peer address at all. Anything
// that distinguishes callers, such as the pre-session rate limit
// (ADR 0101), needs the address the *front door* saw.
package clientaddr

import (
	"context"
	"net"
	"net/http"
	"strings"
)

type contextKey struct{}

// forwardedFor is the header the front door appends the observed client
// address to.
const forwardedFor = "X-Forwarded-For"

// Unknown is the attribution used when no address could be resolved. It is a
// single shared bucket rather than one per empty string, so an in-process
// caller cannot mint unlimited identities by having no address.
const Unknown = "unknown"

// Middleware resolves the client address and puts it in the request context.
//
// trustForwarded decides whether the forwarded header may be believed, and it
// must be true only when the listener cannot be reached except through the
// front door — in practice, when it is a Unix socket. A header is a claim by
// whoever sent it, so believing one on a listener anybody can connect to would
// turn a rate limit into something a caller opts out of by setting a header.
// That is why this is a parameter rather than a lookup: the caller knows what
// it bound, and the decision belongs where that is known.
func Middleware(trustForwarded bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			resolved := resolve(r, trustForwarded)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), contextKey{}, resolved)))
		})
	}
}

// From returns the resolved address, or Unknown when nothing resolved it —
// which includes every path that did not pass through Middleware, such as an
// in-process call in a test.
func From(ctx context.Context) string {
	if addr, ok := ctx.Value(contextKey{}).(string); ok && addr != "" {
		return addr
	}
	return Unknown
}

// resolve picks the address to attribute a request to.
func resolve(r *http.Request, trustForwarded bool) string {
	if trustForwarded {
		if addr := rightmostForwarded(r.Header.Values(forwardedFor)); addr != "" {
			return addr
		}
	}
	return hostOf(r.RemoteAddr)
}

// rightmostForwarded returns the last entry of X-Forwarded-For.
//
// **The rightmost, not the leftmost, and the difference is the whole point.**
// The header is a list, and each proxy appends what *it* observed. Everything
// to the left of the last entry was supplied by the caller and is therefore
// forgeable: a client that sends `X-Forwarded-For: 1.2.3.4` has the front door
// append its real address after it, so the leftmost value is whatever the
// client felt like claiming. With exactly one trusted proxy in front — which
// is what ADR 0120 guarantees — the last entry is the only one the front door
// wrote, and the only one worth believing.
func rightmostForwarded(values []string) string {
	// Multiple header lines are equivalent to one comma-joined line, so flatten
	// before taking the last: a client can send its own X-Forwarded-For line
	// and the proxy's may arrive as a second.
	var all []string
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			if part = strings.TrimSpace(part); part != "" {
				all = append(all, part)
			}
		}
	}
	if len(all) == 0 {
		return ""
	}
	return hostOf(all[len(all)-1])
}

// hostOf strips a port and IPv6 brackets and returns the result only if it is
// actually an IP address, so one caller's many connections share one bucket
// rather than one per ephemeral port.
//
// **Requiring a real IP is what makes a Unix connection resolve to Unknown.**
// Go reports `RemoteAddr` for a socket as the client's socket name, which for
// an unnamed one is `"@"` — a single constant string, identical for every
// caller. Returning it would produce one shared bucket wearing the shape of an
// address: the same failure as an empty key, but harder to notice in a log
// because it looks like something was resolved.
func hostOf(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return ""
	}
	host := addr
	if h, _, err := net.SplitHostPort(addr); err == nil {
		host = h
	} else {
		// A bare IPv6 address has colons and no port, which SplitHostPort
		// rejects.
		host = strings.Trim(addr, "[]")
	}
	if net.ParseIP(host) == nil {
		return ""
	}
	return host
}
