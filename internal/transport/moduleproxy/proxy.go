// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package moduleproxy is the forward proxy an out-of-process module's egress
// goes through (platform#39).
//
// # What it is for
//
// An in-process module is handed an *http.Client that routes through
// netguard's dial guard (platform#33, seam 9), so a module fetching a URL a user
// supplied cannot reach the host's own network — the Platform's PostgreSQL, a
// service on the LAN, the cloud metadata endpoint at 169.254.169.254. That
// client cannot cross a process boundary. This is what replaces it: every
// outbound call an out-of-process module makes goes through a proxy the
// Platform operates, which applies the same deny list and attributes every host
// to the module that contacted it.
//
// # What it sees, and what it does not
//
// It is a CONNECT-style tunnel: for HTTPS it sees the *host* a module contacts
// and never the content, because it does not terminate TLS — terminating a
// third party's TLS to inspect a module's traffic would be disproportionate,
// and host-level attribution is what is actually needed. For plain HTTP there
// is no TLS to leave alone, so a forwarding proxy necessarily sees the request;
// that is a property of unencrypted transport, not a choice, and it is one
// reason a module should prefer HTTPS.
//
// # Convention now, enforcement later
//
// The proxy listens on loopback and the Platform points the module's
// HTTP_PROXY/HTTPS_PROXY at it, so a module using an ordinary HTTP client
// routes through it without any change to the module. That makes it the *easy*
// path but not yet the *only* path: a module that ignored the proxy and dialled
// a target directly would not be stopped, because it still has a network. What
// makes the proxy the only path is denying the module process a network of its
// own (platform#39's layer 3), which is deployment-dependent and lands separately.
//
// The distinction matters for what this closes today. It closes the
// accidental and user-URL SSRF path for a *cooperating* module — which every
// first-party module is: `module-stremio-addons` fetches the addon URLs a user
// typed through the client it is given, so a URL resolving into a private range
// is refused here exactly as it was in process. It does not yet contain a
// module written to be hostile; that is what layer 3 adds, and this record does
// not claim otherwise.
package moduleproxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/transport/netguard"
)

// Proxy is a running forward proxy for one module. One per module makes
// attribution inherent — every connection it handles belongs to that module —
// and keeps a wedged module's egress from being confused with another's.
type Proxy struct {
	moduleID string
	ln       net.Listener
	server   *http.Server
	dialer   func(ctx context.Context, network, address string) (net.Conn, error)
	log      v1.Telemetry

	// onHost is a test seam: it is called with every host the module contacts,
	// so a test can assert traffic actually went through the proxy rather than
	// around it. Nil in production, where telemetry is the record.
	onHost func(host string)
}

// Options configures a proxy.
type Options struct {
	// ModuleID attributes the proxy's telemetry. It is the module's manifest id
	// when known, or a launch-time label before the handshake.
	ModuleID string
	// AllowPrivate is the operator override platform#39 requires for the genuine
	// case of a module sourcing from a service on the local network. Default
	// false: loopback, RFC1918 and link-local targets are refused, which is the
	// posture a fetch of a user-supplied URL needs. Turning it on re-opens the
	// LAN by design, and is a decision an operator makes per deployment.
	AllowPrivate bool
	// Log receives one record per host contacted (seam 9), attributed to the
	// module. Host only for HTTPS; never content. Nil is valid.
	Log v1.Telemetry
	// OnHost is a test seam; leave nil in production.
	OnHost func(host string)
}

// Start begins serving on an ephemeral loopback port and returns the running
// proxy. Close stops it.
func Start(opts Options) (*Proxy, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, fmt.Errorf("moduleproxy: listen: %w", err)
	}

	// The target dial is guarded by default and plain only under the operator
	// override. This is the deny list: netguard.DialContext runs its check after
	// DNS with the concrete address in hand, so a hostname that resolves into a
	// private range is refused rather than dialled — closing the DNS-rebinding
	// variant a name check alone would miss.
	dialer := netguard.DialContext
	if opts.AllowPrivate {
		plain := &net.Dialer{Timeout: 10 * time.Second}
		dialer = plain.DialContext
	}

	p := &Proxy{
		moduleID: opts.ModuleID,
		ln:       ln,
		dialer:   dialer,
		log:      opts.Log,
		onHost:   opts.OnHost,
	}
	p.server = &http.Server{
		Handler:           p,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() { _ = p.server.Serve(ln) }()
	return p, nil
}

// Addr is the proxy URL, for the module's HTTP_PROXY/HTTPS_PROXY.
func (p *Proxy) Addr() string { return "http://" + p.ln.Addr().String() }

// Close stops the proxy.
func (p *Proxy) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.server.Shutdown(ctx)
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.handleConnect(w, r)
		return
	}
	p.handleHTTP(w, r)
}

// handleConnect tunnels an HTTPS connection: it dials the target through the
// deny list and, on success, splices the two connections without ever seeing
// the bytes between them.
func (p *Proxy) handleConnect(w http.ResponseWriter, r *http.Request) {
	host := hostOnly(r.Host)
	p.note(host)

	target, err := p.dialer(r.Context(), "tcp", r.Host)
	if err != nil {
		p.refuse(w, host, err)
		return
	}
	defer func() { _ = target.Close() }()

	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "proxy: connection cannot be hijacked", http.StatusInternalServerError)
		return
	}
	client, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	defer func() { _ = client.Close() }()

	if _, err := client.Write([]byte("HTTP/1.1 200 Connection Established\r\n\r\n")); err != nil {
		return
	}

	// Splice both directions. The proxy copies opaque bytes; for a TLS tunnel
	// that is ciphertext, which is the point — the host was the only thing worth
	// seeing and it was seen before the dial.
	done := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(target, client); done <- struct{}{} }()
	go func() { _, _ = io.Copy(client, target); done <- struct{}{} }()
	<-done
}

// handleHTTP forwards a plain-HTTP request. Unlike CONNECT there is no tunnel:
// the proxy makes the request itself, through the deny-listed dialer, and
// copies the response back.
func (p *Proxy) handleHTTP(w http.ResponseWriter, r *http.Request) {
	if !r.URL.IsAbs() {
		// A non-absolute request URL means the client did not treat this as a
		// proxy. Nothing legitimate reaches the proxy that way.
		http.Error(w, "proxy: expected an absolute request URI", http.StatusBadRequest)
		return
	}
	host := hostOnly(r.URL.Host)
	p.note(host)

	transport := &http.Transport{DialContext: p.dialer}
	outReq := r.Clone(r.Context())
	outReq.RequestURI = ""

	resp, err := transport.RoundTrip(outReq)
	if err != nil {
		p.refuse(w, host, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()

	for k, vs := range resp.Header {
		for _, v := range vs {
			w.Header().Add(k, v)
		}
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// refuse turns a dial failure into the right response and records it. A blocked
// address is a deny-list hit — a 403 the module should treat as "not allowed",
// distinct from a 502 for a host that is permitted but unreachable.
func (p *Proxy) refuse(w http.ResponseWriter, host string, err error) {
	if errors.Is(err, netguard.ErrBlockedAddress) {
		if p.log != nil {
			p.log.Warn("module egress denied",
				v1.String("module", p.moduleID),
				v1.String("host", host))
		}
		http.Error(w, "proxy: destination is not a permitted address", http.StatusForbidden)
		return
	}
	http.Error(w, "proxy: upstream unreachable", http.StatusBadGateway)
}

// note records a host the module contacted — the per-module attribution seam 9
// exists for. Host only; never the path or the content.
func (p *Proxy) note(host string) {
	if p.onHost != nil {
		p.onHost(host)
	}
	if p.log != nil {
		p.log.Info("module egress",
			v1.String("module", p.moduleID),
			v1.String("host", host))
	}
}

// hostOnly strips a port from a host:port, leaving just the host for
// attribution. A bare host is returned unchanged.
func hostOnly(hostport string) string {
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		return h
	}
	return hostport
}
