// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package artwork is the Platform's artwork proxy (ADR 0030). Module metadata
// carries poster/backdrop URLs on third-party CDNs; serving those to a client
// directly is fragile — a CDN without CORS headers breaks the artlight canvas
// and can break the image entirely, and it leaks the viewer's IP to the CDN.
// The proxy fetches such an image and re-serves it from the Platform's own
// origin with permissive CORS, so a client always gets a same-origin (or
// CORS-enabled) URL.
//
// This is the *virtual-plane* half (ADR 0028): nothing is stored durably, so
// uncurated artwork never accumulates. Durable caching of a materialised item's
// chosen artwork is a separate slice.
//
// Two safeguards make an open `?url=` proxy safe: every URL the Platform emits
// is HMAC-signed, so the proxy fetches only URLs it produced; and the dialer
// refuses to connect to loopback, private or link-local addresses (checked at
// connect time, after DNS, so a rebinding trick cannot slip past), closing the
// SSRF hole a naive proxy would open.
package artwork

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

// maxImageBytes caps a proxied response so a hostile source cannot stream an
// unbounded body through the Platform.
const maxImageBytes = 16 << 20 // 16 MiB

// Signer signs and verifies the artwork URLs the Platform emits. The key is
// process-scoped: screens are re-fetched, so a signature need not outlive the
// process that produced it.
type Signer struct {
	key []byte
}

// NewSigner builds a Signer over a secret key.
func NewSigner(key []byte) *Signer { return &Signer{key: key} }

func (s *Signer) sign(raw string) string {
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(raw))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func (s *Signer) verify(raw, sig string) bool {
	want, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil {
		return false
	}
	m := hmac.New(sha256.New, s.key)
	m.Write([]byte(raw))
	return hmac.Equal(want, m.Sum(nil))
}

// Rewrite turns a remote http(s) artwork URL into a Platform-relative proxy URL
// (`/artwork?u=…&s=…`). A non-http(s) or empty URL is returned unchanged, so an
// already-local or absent poster passes through. Relative output means the
// client fetches it same-origin, which is what makes the artlight canvas
// readable without any CORS at all.
func (s *Signer) Rewrite(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") {
		return raw
	}
	q := url.Values{"u": {raw}, "s": {s.sign(raw)}}
	return "/artwork?" + q.Encode()
}

// GuardedClient is the SSRF-safe HTTP client the proxy fetches with in
// production: a timeout, and a dialer that refuses non-public addresses.
func GuardedClient() *http.Client {
	return &http.Client{
		Timeout:   15 * time.Second,
		Transport: &http.Transport{DialContext: guardedDial},
	}
}

// Handler serves the proxy over client. It verifies the signature, then fetches
// the remote image and streams it back with permissive CORS and a long cache
// lifetime (the bytes behind a signed URL do not change). Pass GuardedClient()
// in production; a test may pass a plain client to reach a loopback fixture.
func Handler(signer *Signer, client *http.Client) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := r.URL.Query().Get("u")
		sig := r.URL.Query().Get("s")
		if raw == "" || sig == "" || !signer.verify(raw, sig) {
			http.Error(w, "invalid artwork request", http.StatusForbidden)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, raw, nil)
		if err != nil {
			http.Error(w, "bad url", http.StatusBadRequest)
			return
		}
		// A courteous, non-bot UA — some CDNs 403 the default Go agent.
		req.Header.Set("User-Agent", "mosaic-platform-artwork/1")

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream fetch failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			http.Error(w, "upstream status", http.StatusBadGateway)
			return
		}
		ct := resp.Header.Get("Content-Type")
		if !strings.HasPrefix(ct, "image/") {
			http.Error(w, "not an image", http.StatusBadGateway)
			return
		}

		w.Header().Set("Content-Type", ct)
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = io.Copy(w, io.LimitReader(resp.Body, maxImageBytes))
	})
}

// errBlockedAddress is returned by the dial guard for a non-routable target.
var errBlockedAddress = errors.New("artwork: refusing to connect to a non-public address")

// guardedDial dials only public addresses. The Control hook runs after DNS with
// the concrete address about to be connected, so a hostname that resolves (or
// re-resolves) to a private range is caught here, closing the SSRF/DNS-rebinding
// hole a plain proxy would leave open.
func guardedDial(ctx context.Context, network, address string) (net.Conn, error) {
	d := &net.Dialer{
		Timeout: 10 * time.Second,
		Control: func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			ip := net.ParseIP(host)
			if ip == nil || blocked(ip) {
				return errBlockedAddress
			}
			return nil
		},
	}
	return d.DialContext(ctx, network, address)
}

// blocked reports whether an IP is one the proxy must not reach: loopback,
// private, link-local (incl. the cloud metadata address 169.254.169.254),
// unspecified, or otherwise not global-unicast.
func blocked(ip net.IP) bool {
	return ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() || ip.IsUnspecified() || !ip.IsGlobalUnicast()
}
