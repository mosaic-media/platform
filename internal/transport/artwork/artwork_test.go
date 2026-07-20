// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package artwork

import (
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestSignerRoundTripAndTamper(t *testing.T) {
	s := NewSigner([]byte("k"))
	raw := "https://cdn.example/poster.jpg"
	sig := s.sign(raw)
	if !s.verify(raw, sig) {
		t.Fatal("a genuine signature must verify")
	}
	if s.verify(raw, sig+"x") {
		t.Fatal("a tampered signature must not verify")
	}
	if s.verify(raw+"?evil", sig) {
		t.Fatal("a signature must not carry to a different url")
	}
}

func TestRewrite(t *testing.T) {
	s := NewSigner([]byte("k"))
	// An http(s) URL is proxied.
	got := s.Rewrite("https://images.metahub.space/poster/small/tt1/img")
	if !strings.HasPrefix(got, "/artwork?") || !strings.Contains(got, "s=") {
		t.Fatalf("rewrite = %q, want a signed /artwork url", got)
	}
	// It verifies against the signer.
	u, _ := url.Parse(got)
	if !s.verify(u.Query().Get("u"), u.Query().Get("s")) {
		t.Fatal("rewritten url must carry a valid signature")
	}
	// Empty and non-http pass through unchanged.
	if s.Rewrite("") != "" {
		t.Fatal("empty url must pass through")
	}
	if s.Rewrite("data:image/png;base64,AAAA") != "data:image/png;base64,AAAA" {
		t.Fatal("a non-http url must pass through unchanged")
	}
}

func TestBlockedAddresses(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1":       true,  // loopback
		"::1":             true,  // loopback v6
		"10.1.2.3":        true,  // private
		"192.168.0.1":     true,  // private
		"172.16.5.4":      true,  // private
		"169.254.169.254": true,  // link-local (cloud metadata)
		"0.0.0.0":         true,  // unspecified
		"8.8.8.8":         false, // public
		"1.1.1.1":         false, // public
	}
	for ipStr, want := range cases {
		if got := blocked(net.ParseIP(ipStr)); got != want {
			t.Errorf("blocked(%s) = %v, want %v", ipStr, got, want)
		}
	}
}

func TestHandlerServesSignedImageAndRejectsTheRest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/img":
			w.Header().Set("Content-Type", "image/png")
			_, _ = w.Write([]byte("\x89PNG\r\n\x1a\n" + "fake-bytes"))
		default:
			w.Header().Set("Content-Type", "text/plain")
			_, _ = w.Write([]byte("not an image"))
		}
	}))
	defer upstream.Close()

	signer := NewSigner([]byte("k"))
	// A plain client reaches the loopback fixture (the guarded client would
	// refuse it — which is the point of the guard, tested separately above).
	h := Handler(signer, upstream.Client())

	// A properly signed image request succeeds and is served with CORS.
	proxied := signer.Rewrite(upstream.URL + "/img")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, proxied, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("signed image: status %d, want 200", rec.Code)
	}
	if rec.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("content-type = %q, want image/png", rec.Header().Get("Content-Type"))
	}
	if rec.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("proxied artwork must be served with permissive CORS")
	}

	// An unsigned request is refused.
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/artwork?u="+url.QueryEscape(upstream.URL+"/img"), nil))
	if rec.Code != http.StatusForbidden {
		t.Fatalf("unsigned request: status %d, want 403", rec.Code)
	}

	// A signed but non-image upstream is refused (the proxy serves images only).
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, signer.Rewrite(upstream.URL+"/txt"), nil))
	if rec.Code != http.StatusBadGateway {
		t.Fatalf("non-image upstream: status %d, want 502", rec.Code)
	}
}
