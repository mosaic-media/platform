// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package playback is the Platform's media origin (platform#25). A playback module
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
// The ticket is **sealed, not signed**. The artwork proxy (platform#20) signs a URL
// it puts in the query string, which is fine for a public image; here the
// payload is exactly the secret being protected, so it is encrypted rather than
// authenticated-in-the-clear.
package playback

import (
	"context"
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
	// Plan is the per-stream decision (platform#29): which video and audio streams
	// travel and whether each is copied or re-encoded. It is decided once, at
	// mint time, with the probe results in hand — not re-derived on every range
	// request, which for a seeking player would mean probing dozens of times.
	Plan    Plan  `json:"p,omitempty"`
	Expires int64 `json:"e"`

	// PartID and Class are what the origin needs to ask the source again
	// (platform#28). A cached upstream address is perishable with no trustworthy
	// expiry — a debrid link dies the moment its torrent leaves the provider's
	// cache, whatever its age — so the answer to a dead one is to re-resolve
	// rather than to fail the play, and re-resolving needs the release and the
	// capability class the address was cached under.
	//
	// Both are absent on a ticket minted without them, and the origin then
	// behaves exactly as it did before this existed: one attempt, and an honest
	// failure if it does not work.
	PartID string `json:"pid,omitempty"`
	Class  string `json:"cls,omitempty"`
}

// TicketOption is extra detail sealed into a ticket.
//
// Variadic rather than more parameters because the two things it carries are
// optional in a real sense: a ticket without them still plays, it just cannot
// recover from a dead link. Growing Mint's signature would have made twenty
// call sites state that they have nothing to say.
type TicketOption func(*ticket)

// For records which release and capability class an address was resolved for,
// so the origin can re-ask when it stops working (platform#28).
func For(partID, class string) TicketOption {
	return func(t *ticket) { t.PartID, t.Class = partID, class }
}

// Resolver re-asks the source where a release's bytes are.
//
// It is the origin's one call back into the application services, and it is an
// interface here rather than a concrete type for the reason every transport
// boundary is: a transport calls services, and naming one by its interface is
// what keeps that checkable. The composition root supplies the implementation.
//
// It takes a session rather than a caller because a ticket carries one: the
// re-resolve runs as whoever minted the ticket, so it is authorised on exactly
// the same terms as the play that produced it.
type Resolver interface {
	ReresolvePlayback(ctx context.Context, session, partID, class string) (url string, headers map[string]string, err error)
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
func (s *Sealer) Mint(url string, headers map[string]string, session string, plan Plan, opts ...TicketOption) (string, error) {
	t := ticket{
		URL:     url,
		Headers: headers,
		Session: session,
		Plan:    plan,
		Expires: s.now().Add(TicketTTL).Unix(),
	}
	for _, opt := range opts {
		opt(&t)
	}
	payload, err := json.Marshal(t)
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
// It keeps a session registry nobody else can reach, so **nothing reaps or
// stops the transcodes it starts**. That is fine for a test, whose process ends
// in a moment, and wrong for a serving Platform: a viewer who closes the tab
// leaves ffmpeg running and its spool on disk with no request left in which to
// notice. A composition root builds its own registry and calls
// HandlerWithSessions.
func Handler(sealer *Sealer, client *http.Client, remuxer *Remuxer) http.Handler {
	return HandlerWithSessions(sealer, client, remuxer, NewSegmentSessions(DefaultSegmentDir))
}

// resolver is the re-resolve the origin will use, set by the composition root.
//
// A package variable rather than a parameter on Handler, because every other
// constructor here is already four arguments wide and this is one optional
// collaborator that the whole package shares. A nil resolver is the honest
// no-op: the origin then behaves exactly as it did before invalidate-on-read
// existed, which is what a test that has not set one wants.
var resolver Resolver

// SetResolver gives the origin a way to re-ask the source for a dead link
// (platform#28). Called once from the composition root.
func SetResolver(r Resolver) { resolver = r }

// refreshed re-resolves a ticket's upstream address and reports whether it
// changed to something worth retrying.
//
// **The old address is not compared against the new one for equality alone.** A
// source that hands back the identical URL has told the origin the link is not
// the problem, and retrying it would be a second identical failure; a source
// that hands back a different one has repaired exactly the failure this exists
// for. So an unchanged address is treated as no answer.
func refreshed(ctx context.Context, t ticket) (ticket, bool) {
	if resolver == nil || t.PartID == "" || t.Class == "" {
		return t, false
	}
	url, headers, err := resolver.ReresolvePlayback(ctx, t.Session, t.PartID, t.Class)
	if err != nil || url == "" || url == t.URL {
		return t, false
	}
	t.URL, t.Headers = url, headers
	return t, true
}

// HandlerWithSessions is Handler over an explicit session registry, so the
// caller can start its reaper and stop every running transcode on shutdown.
// **This is the production constructor**; see Handler for what holding no
// reference costs.
func HandlerWithSessions(sealer *Sealer, client *http.Client, remuxer *Remuxer, sessions *SegmentSessions) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}

		// The path is either a ticket on its own — the relayed stream — or a
		// ticket and one HLS resource under it. Exactly one slash is allowed,
		// and the resource is matched against a closed set below rather than
		// used as a filename.
		rest := strings.TrimPrefix(r.URL.Path, "/playback/")
		raw, resource, _ := strings.Cut(rest, "/")
		if raw == "" || strings.Contains(resource, "/") {
			http.Error(w, "invalid playback request", http.StatusForbidden)
			return
		}
		t, err := sealer.open(raw)
		if err != nil {
			http.Error(w, "invalid playback request", http.StatusForbidden)
			return
		}

		// Anything the client cannot decode as-is goes through ffmpeg; anything
		// it can is relayed untouched, which keeps byte-range seeking and costs
		// no CPU. The branch is here rather than in the module because this is a
		// transform on the serving side, and a module never serves (platform#25).
		if !t.Plan.DirectPlay {
			if !remuxer.Available() {
				http.Error(w, "this release needs re-encoding ("+t.Plan.Reason+") and ffmpeg is not installed", http.StatusNotImplemented)
				return
			}
			serveSegmented(w, r, remuxer, sessions, t, raw, resource)
			return
		}
		if resource != "" {
			// A relayed stream has one sub-resource and only one: a subtitle file
			// a module found elsewhere (platform#72). It is not in the release, so
			// whether the video needed a transcode has no bearing on it — which
			// is exactly why a direct-played release can have subtitles at all,
			// where an embedded track cannot (platform#68).
			if i, ok := externalSubtitleOf(resource); ok && i < len(t.Plan.External) {
				serveExternalSubtitle(w, r, remuxer, t.Plan.External[i].URL)
				return
			}
			// Anything else: the relayed stream is the upstream's own bytes at
			// the ticket itself.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}

		// The relayed fetch, retried once against a re-resolved address
		// (platform#28). This is the cleanest place in the origin to do it: the
		// upstream is contacted before a byte is written to the client, so a
		// dead link can be repaired with nothing committed to the response.
		resp, err := relayFetch(r, client, t)
		if err != nil || resp.StatusCode >= 400 {
			if resp != nil {
				resp.Body.Close()
			}
			if fresh, ok := refreshed(r.Context(), t); ok {
				t = fresh
				resp, err = relayFetch(r, client, t)
			}
		}
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

// relayFetch performs one upstream request for a relayed stream.
//
// Split out of the handler so the same request can be made twice — once against
// the cached address and once against a re-resolved one — without the header
// assembly drifting between the two attempts.
func relayFetch(r *http.Request, client *http.Client, t ticket) (*http.Response, error) {
	req, err := http.NewRequestWithContext(r.Context(), r.Method, t.URL, nil)
	if err != nil {
		return nil, err
	}
	// The module's headers first — they are what the upstream requires — then
	// the client's range, which must not be overridable by them.
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
	return client.Do(req)
}
