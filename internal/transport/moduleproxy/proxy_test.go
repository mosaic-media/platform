// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package moduleproxy_test

import (
	"crypto/tls"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync"
	"testing"

	"github.com/mosaic-media/platform/internal/transport/moduleproxy"
)

// clientThrough builds an HTTP client whose requests go through the proxy, the
// way the module process's HTTP_PROXY env var makes them.
func clientThrough(t *testing.T, p *moduleproxy.Proxy) *http.Client {
	t.Helper()
	proxyURL, err := url.Parse(p.Addr())
	if err != nil {
		t.Fatalf("parsing proxy addr: %v", err)
	}
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
			// The test's target servers use self-signed certs; a real module
			// contacting a real host does not need this, but the proxy is a
			// tunnel and does not care either way — it never sees the TLS.
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // test target only
		},
	}
}

// hostRecorder captures the hosts the proxy reports, so a test can assert
// traffic went through it rather than around it.
type hostRecorder struct {
	mu    sync.Mutex
	hosts []string
}

func (h *hostRecorder) record(host string) {
	h.mu.Lock()
	h.hosts = append(h.hosts, host)
	h.mu.Unlock()
}

func (h *hostRecorder) saw(host string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, s := range h.hosts {
		if s == host {
			return true
		}
	}
	return false
}

// TestProxyTunnelsAndAttributesAnAllowedHost pins that with the operator
// override on, a module reaches a permitted target and the proxy attributes the
// host — the seam-9 property, over a real CONNECT tunnel.
func TestProxyTunnelsAndAttributesAnAllowedHost(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "ok")
	}))
	defer target.Close()

	rec := &hostRecorder{}
	// AllowPrivate because the test target is on loopback — the same override an
	// operator sets for a LAN addon.
	p, err := moduleproxy.Start(moduleproxy.Options{
		ModuleID: "test", AllowPrivate: true, OnHost: rec.record,
	})
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer p.Close()

	resp, err := clientThrough(t, p).Get(target.URL)
	if err != nil {
		t.Fatalf("request through proxy: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body through proxy: got %q, want ok", body)
	}

	host := hostOf(t, target.URL)
	if !rec.saw(host) {
		t.Errorf("the proxy did not attribute host %q — did the request go through it?", host)
	}
}

// TestProxyRefusesAPrivateTargetByDefault pins that with the override off — the
// default — a loopback target is refused. This is the deny list: a module
// fetching a user-supplied URL that resolves into a private range cannot reach
// it.
func TestProxyRefusesAPrivateTargetByDefault(t *testing.T) {
	target := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "should not be reachable")
	}))
	defer target.Close()

	p, err := moduleproxy.Start(moduleproxy.Options{ModuleID: "test"}) // AllowPrivate defaults false
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer p.Close()

	// A CONNECT to a loopback target is refused at the proxy: the tunnel dial
	// hits the deny list, so the client's request fails rather than reaching the
	// server.
	resp, err := clientThrough(t, p).Get(target.URL)
	if err == nil {
		defer resp.Body.Close()
		t.Fatalf("a private target was reachable through the proxy (status %d)", resp.StatusCode)
	}
}

// TestProxyForwardsPlainHTTPAndAppliesTheDenyList pins that plain HTTP is
// forwarded too, and the deny list applies there as well — a module using
// http:// rather than https:// is not a way around it.
func TestProxyForwardsPlainHTTPAndAppliesTheDenyList(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, "plain ok")
	}))
	defer target.Close()

	// Allowed with the override.
	rec := &hostRecorder{}
	allow, err := moduleproxy.Start(moduleproxy.Options{ModuleID: "t", AllowPrivate: true, OnHost: rec.record})
	if err != nil {
		t.Fatalf("start proxy: %v", err)
	}
	defer allow.Close()
	resp, err := clientThrough(t, allow).Get(target.URL)
	if err != nil {
		t.Fatalf("plain HTTP through proxy: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	if string(body) != "plain ok" {
		t.Errorf("plain body: got %q", body)
	}
	if !rec.saw(hostOf(t, target.URL)) {
		t.Error("plain HTTP did not go through the proxy")
	}

	// Refused by default.
	deny, err := moduleproxy.Start(moduleproxy.Options{ModuleID: "t"})
	if err != nil {
		t.Fatalf("start deny proxy: %v", err)
	}
	defer deny.Close()
	resp2, err := clientThrough(t, deny).Get(target.URL)
	if err == nil {
		defer resp2.Body.Close()
		if resp2.StatusCode != http.StatusForbidden {
			t.Errorf("plain HTTP to a private target: got status %d, want a refusal", resp2.StatusCode)
		}
	}
}

func hostOf(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse url %q: %v", raw, err)
	}
	return u.Hostname()
}
