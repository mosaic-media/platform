// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package playback is the Platform's media origin (ADR 0045). A playback module
// resolves a Part to an upstream location; this serves those bytes from the
// Platform's own origin, so a client fetches a same-origin, range-capable URL
// and never sees where the bytes actually came from.
//
// Why the Platform is in the byte path at all, since relaying is not free: the
// upstream is typically a debrid CDN link carrying a credential in its URL or
// headers, and handing that to a browser publishes it. Relaying also keeps the
// viewer's IP off the CDN and keeps one consistent origin for range requests. It
// costs one ingress leg and one egress leg — the same egress as serving a local
// file, not double. Letting a client fetch upstream directly is a deliberate
// future opt-out for the local-network case, not the default.
//
// The ticket is **sealed, not signed**. The artwork proxy (ADR 0030) signs a URL
// it puts in the query string, which is fine for a public image; here the
// payload is exactly the secret being protected, so it is encrypted rather than
// authenticated-in-the-clear.
package playback

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/mosaic-media/platform/internal/transport/netguard"
)

// TicketTTL bounds how long a minted ticket stays valid. It has to cover one
// sitting — a long film, paused partway through — and no more, since a session
// that resumes the next day should re-resolve anyway: the upstream link behind
// the ticket has its own, usually shorter, expiry, so a ticket outliving it
// buys nothing but a worse error.
const TicketTTL = 6 * time.Hour

// ticket is the sealed payload. It never reaches a client in readable form.
type ticket struct {
	URL     string            `json:"u"`
	Headers map[string]string `json:"h,omitempty"`
	// Session is the session the ticket was minted for. It is carried so a
	// leaked ticket is attributable and so revocation can key on it later;
	// today's handler enforces only the expiry, which is stated plainly here
	// rather than described as binding it does not do.
	Session string `json:"s,omitempty"`
	// Plan is the per-stream decision (ADR 0050): which video and audio streams
	// travel and whether each is copied or re-encoded. It is decided once, at
	// mint time, with the probe results in hand — not re-derived on every range
	// request, which for a seeking player would mean probing dozens of times.
	Plan    Plan  `json:"p,omitempty"`
	Expires int64 `json:"e"`
}

// Sealer mints and opens playback tickets. Its key is process-scoped, like the
// artwork signer's: a ticket outlives neither the process nor its TTL, and a
// restart re-resolving playback is correct rather than merely acceptable.
type Sealer struct {
	aead cipher.AEAD
	now  func() time.Time
}

// NewSealer builds a Sealer over a 16-, 24- or 32-byte key.
func NewSealer(key []byte) (*Sealer, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Sealer{aead: aead, now: time.Now}, nil
}

// ErrInvalidTicket covers every reason a ticket will not open — malformed,
// tampered with, or expired. They are deliberately one error: distinguishing
// them for the caller would tell an attacker which part of a guess was right.
var ErrInvalidTicket = errors.New("playback: invalid ticket")

// Mint seals an upstream location into an opaque ticket string, safe to put in
// a URL path.
func (s *Sealer) Mint(url string, headers map[string]string, session string, plan Plan) (string, error) {
	payload, err := json.Marshal(ticket{
		URL:     url,
		Headers: headers,
		Session: session,
		Plan:    plan,
		Expires: s.now().Add(TicketTTL).Unix(),
	})
	if err != nil {
		return "", err
	}
	nonce := make([]byte, s.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := s.aead.Seal(nonce, nonce, payload, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

// open reverses Mint, rejecting anything that will not decrypt or has expired.
func (s *Sealer) open(raw string) (ticket, error) {
	sealed, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil || len(sealed) < s.aead.NonceSize() {
		return ticket{}, ErrInvalidTicket
	}
	nonce, body := sealed[:s.aead.NonceSize()], sealed[s.aead.NonceSize():]
	payload, err := s.aead.Open(nil, nonce, body, nil)
	if err != nil {
		return ticket{}, ErrInvalidTicket
	}
	var t ticket
	if err := json.Unmarshal(payload, &t); err != nil {
		return ticket{}, ErrInvalidTicket
	}
	if s.now().Unix() > t.Expires {
		return ticket{}, ErrInvalidTicket
	}
	return t, nil
}

// Client is the HTTP client the origin fetches upstream with.
//
// It deliberately sets no overall Timeout: a client.Timeout covers the whole
// response body, so any value at all would cut a film off mid-play. The bounds
// that matter for a stream are on establishing the connection and on the
// upstream taking too long to *start* responding, which is what
// ResponseHeaderTimeout gives.
func Client() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			DialContext:           netguard.DialContext,
			ResponseHeaderTimeout: 30 * time.Second,
			IdleConnTimeout:       90 * time.Second,
		},
	}
}

// relayedRequestHeaders are the client headers forwarded upstream. Range and
// If-Range are the point of the list — without them seeking does not work, and
// a browser will refuse to play a source it cannot range-request.
var relayedRequestHeaders = []string{"Range", "If-Range", "If-Modified-Since", "If-None-Match"}

// relayedResponseHeaders are the upstream headers passed back. Content-Range and
// Accept-Ranges are what tell the player the stream is seekable.
var relayedResponseHeaders = []string{
	"Content-Type", "Content-Length", "Content-Range", "Accept-Ranges",
	"Last-Modified", "ETag", "Content-Disposition",
}

// Handler serves the origin at /playback/{ticket}. It opens the ticket, fetches
// the upstream location with the client's range intact, and relays the response
// through — status included, so a 206 stays a 206.
//
// Pass Client() in production; a test may pass a plain client to reach a
// loopback fixture, exactly as the artwork proxy does.
func Handler(sealer *Sealer, client *http.Client, remuxer *Remuxer) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		raw := strings.TrimPrefix(r.URL.Path, "/playback/")
		if raw == "" || strings.Contains(raw, "/") {
			http.Error(w, "invalid playback request", http.StatusForbidden)
			return
		}
		t, err := sealer.open(raw)
		if err != nil {
			http.Error(w, "invalid playback request", http.StatusForbidden)
			return
		}

		// Anything the client cannot decode as-is goes through ffmpeg; anything
		// it can is relayed untouched, which keeps byte-range seeking. The
		// branch is here rather than in the module because this is a transform
		// on the serving side, and a module never serves (ADR 0045).
		if !t.Plan.DirectPlay {
			if !remuxer.Available() {
				http.Error(w, "this release needs re-encoding ("+t.Plan.Reason+") and ffmpeg is not installed", http.StatusNotImplemented)
				return
			}
			serveRemuxed(w, r, remuxer, t, t.Plan)
			return
		}

		req, err := http.NewRequestWithContext(r.Context(), r.Method, t.URL, nil)
		if err != nil {
			http.Error(w, "bad upstream url", http.StatusBadGateway)
			return
		}
		// The module's headers first — they are what the upstream requires —
		// then the client's range, which must not be overridable by them.
		for k, v := range t.Headers {
			req.Header.Set(k, v)
		}
		for _, h := range relayedRequestHeaders {
			if v := r.Header.Get(h); v != "" {
				req.Header.Set(h, v)
			}
		}
		if req.Header.Get("User-Agent") == "" {
			req.Header.Set("User-Agent", "mosaic-platform-playback/1")
		}

		resp, err := client.Do(req)
		if err != nil {
			http.Error(w, "upstream fetch failed", http.StatusBadGateway)
			return
		}
		defer resp.Body.Close()

		for _, h := range relayedResponseHeaders {
			if v := resp.Header.Get(h); v != "" {
				w.Header().Set(h, v)
			}
		}
		// Media bytes are not cached at this origin: the ticket is short-lived
		// and the upstream behind it is shorter still, so a cached response
		// would outlive the thing it points at.
		w.Header().Set("Cache-Control", "no-store")
		if w.Header().Get("Accept-Ranges") == "" {
			w.Header().Set("Accept-Ranges", "bytes")
		}
		w.WriteHeader(resp.StatusCode)
		if r.Method == http.MethodHead {
			return
		}
		// No LimitReader here, unlike the artwork proxy: a film legitimately
		// runs to tens of gigabytes, and the bound that matters is the ticket's
		// lifetime rather than a byte count.
		_, _ = io.Copy(w, resp.Body)
	})
}
