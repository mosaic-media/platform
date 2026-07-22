// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func newTestSealer(t *testing.T) *Sealer {
	t.Helper()
	s, err := NewSealer([]byte("0123456789abcdef0123456789abcdef"))
	if err != nil {
		t.Fatalf("NewSealer: %v", err)
	}
	return s
}

// TestTicketRoundTripAndTamper is the security core: a ticket must carry its
// payload back intact, must not be readable by whoever holds it, and must not
// open if altered or expired.
func TestTicketRoundTripAndTamper(t *testing.T) {
	s := newTestSealer(t)
	const upstream = "https://cdn.example/movie.mkv?token=supersecret"

	raw, err := s.Mint(upstream, map[string]string{"Authorization": "Bearer abc"}, "session-1", Plan{DirectPlay: true})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// The whole reason this is sealed rather than signed: the ticket a client
	// holds must not disclose the credentialed upstream URL.
	if strings.Contains(raw, "cdn.example") || strings.Contains(raw, "supersecret") {
		t.Fatal("ticket leaked the upstream URL in clear text")
	}

	got, err := s.open(raw)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if got.URL != upstream {
		t.Errorf("URL = %q, want %q", got.URL, upstream)
	}
	if got.Headers["Authorization"] != "Bearer abc" {
		t.Errorf("Headers lost the authorization: %v", got.Headers)
	}
	if got.Session != "session-1" {
		t.Errorf("Session = %q, want %q", got.Session, "session-1")
	}

	// Flipping any byte must fail authentication rather than decode to garbage.
	bad := []byte(raw)
	bad[len(bad)-1] ^= 'x'
	if _, err := s.open(string(bad)); err == nil {
		t.Error("a tampered ticket opened")
	}
	if _, err := s.open("not-a-ticket"); err == nil {
		t.Error("a malformed ticket opened")
	}
}

func TestExpiredTicketDoesNotOpen(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mp4", nil, "session-1", Plan{DirectPlay: true})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if _, err := s.open(raw); err != nil {
		t.Fatalf("fresh ticket should open: %v", err)
	}

	s.now = func() time.Time { return time.Now().Add(TicketTTL + time.Minute) }
	if _, err := s.open(raw); err == nil {
		t.Error("an expired ticket opened")
	}
}

// TestHandlerRelaysRangeRequests is the behaviour a player depends on: a range
// request must reach the upstream and its 206 must come back with the range
// headers intact, or seeking silently does not work.
func TestHandlerRelaysRangeRequests(t *testing.T) {
	const body = "0123456789abcdefghij"

	var gotRange, gotAuth string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRange = r.Header.Get("Range")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "video/mp4")
		w.Header().Set("Accept-Ranges", "bytes")
		if gotRange == "bytes=4-9" {
			w.Header().Set("Content-Range", "bytes 4-9/20")
			w.WriteHeader(http.StatusPartialContent)
			_, _ = io.WriteString(w, body[4:10])
			return
		}
		_, _ = io.WriteString(w, body)
	}))
	defer upstream.Close()

	s := newTestSealer(t)
	raw, err := s.Mint(upstream.URL+"/movie.mp4", map[string]string{"Authorization": "Bearer abc"}, "session-1", Plan{DirectPlay: true})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	// A plain client, so the fixture on loopback is reachable — the guarded
	// dialer in Client() would (correctly) refuse it.
	h := Handler(s, upstream.Client(), NewRemuxerAt(""))

	req := httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil)
	req.Header.Set("Range", "bytes=4-9")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusPartialContent {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusPartialContent)
	}
	if gotRange != "bytes=4-9" {
		t.Errorf("upstream saw Range %q, want %q", gotRange, "bytes=4-9")
	}
	if gotAuth != "Bearer abc" {
		t.Errorf("upstream saw Authorization %q, want the module's header", gotAuth)
	}
	if got := rec.Header().Get("Content-Range"); got != "bytes 4-9/20" {
		t.Errorf("Content-Range = %q, want %q", got, "bytes 4-9/20")
	}
	if got := rec.Body.String(); got != body[4:10] {
		t.Errorf("body = %q, want %q", got, body[4:10])
	}
	if got := rec.Header().Get("Cache-Control"); got != "no-store" {
		t.Errorf("Cache-Control = %q, want no-store", got)
	}
}

// TestHandlerRejectsBadTickets guards the open-proxy hole: without this the
// origin would fetch any URL anyone asked it to.
func TestHandlerRejectsBadTickets(t *testing.T) {
	s := newTestSealer(t)
	h := Handler(s, http.DefaultClient, NewRemuxerAt(""))

	for _, path := range []string{"/playback/", "/playback/garbage", "/playback/a/b"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusForbidden {
			t.Errorf("GET %s status = %d, want %d", path, rec.Code, http.StatusForbidden)
		}
	}

	rec := httptest.NewRecorder()
	raw, _ := s.Mint("https://cdn.example/x.mp4", nil, "s", Plan{DirectPlay: true})
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/playback/"+raw, nil))
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST status = %d, want %d", rec.Code, http.StatusMethodNotAllowed)
	}
}
