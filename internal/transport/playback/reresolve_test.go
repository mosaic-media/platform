// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Invalidate-on-read (ADR 0049): a cached upstream address is perishable with no
// trustworthy expiry, so the answer to a dead one is to ask the source again
// rather than to fail the play.

// fakeResolver answers with a fixed address and counts how often it was asked.
type fakeResolver struct {
	url     string
	err     error
	calls   int
	session string
	partID  string
	class   string
}

func (f *fakeResolver) ReresolvePlayback(_ context.Context, session, partID, class string) (string, map[string]string, error) {
	f.calls++
	f.session, f.partID, f.class = session, partID, class
	if f.err != nil {
		return "", nil, f.err
	}
	return f.url, map[string]string{"X-Fresh": "1"}, nil
}

// withResolver installs one for the duration of a test. The origin's resolver is
// package-scoped, so it has to be restored or the next test inherits it.
func withResolver(t *testing.T, r Resolver) {
	t.Helper()
	previous := resolver
	resolver = r
	t.Cleanup(func() { resolver = previous })
}

// TestADeadLinkIsReResolvedAndRetried is the property the whole slice exists
// for. The first upstream answers 404 — which is what a debrid link looks like
// once its torrent has left the provider's cache — and the viewer must not see
// it.
func TestADeadLinkIsReResolvedAndRetried(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("X-Fresh"); got != "1" {
			t.Errorf("retry did not carry the re-resolved headers: %q", got)
		}
		_, _ = w.Write([]byte("the film"))
	}))
	defer live.Close()
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()

	fake := &fakeResolver{url: live.URL}
	withResolver(t, fake)

	s := newTestSealer(t)
	raw, err := s.Mint(dead.URL, nil, "session-1", Plan{DirectPlay: true}, For("part-7", "browser"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt("")).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — a dead link the origin can repair must not reach the viewer", rec.Code)
	}
	if rec.Body.String() != "the film" {
		t.Errorf("body = %q, want the bytes from the re-resolved address", rec.Body.String())
	}
	if fake.calls != 1 {
		t.Errorf("re-resolved %d times, want exactly once — a retry loop is worse than a failure", fake.calls)
	}
	// It re-asks for the release and class the ticket was minted for, as the
	// session that minted it. Getting any of the three wrong would resolve
	// somebody else's release, or fail the boundary.
	if fake.partID != "part-7" || fake.class != "browser" || fake.session != "session-1" {
		t.Errorf("re-resolved for %q/%q as %q, want part-7/browser as session-1", fake.partID, fake.class, fake.session)
	}
}

// TestAWorkingLinkIsNeverReResolved is the cost bound. ADR 0049 rejects a
// liveness pre-check because it spends a round trip on every play to catch a
// rare failure; a re-resolve on the happy path would be the same mistake with
// an extra network hop.
func TestAWorkingLinkIsNeverReResolved(t *testing.T) {
	live := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("the film"))
	}))
	defer live.Close()

	fake := &fakeResolver{url: "http://unused.invalid"}
	withResolver(t, fake)

	s := newTestSealer(t)
	raw, err := s.Mint(live.URL, nil, "session-1", Plan{DirectPlay: true}, For("part-7", "browser"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt("")).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if fake.calls != 0 {
		t.Errorf("re-resolved %d times on a working link; the happy path must cost nothing", fake.calls)
	}
}

// TestAnUnrepairableLinkStillFails covers the three ways there is nothing to
// retry with. Each must end in the honest failure rather than in a loop or a
// success the origin cannot back up.
func TestAnUnrepairableLinkStillFails(t *testing.T) {
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()

	for name, r := range map[string]Resolver{
		// The source cannot answer either.
		"resolver fails": &fakeResolver{err: errors.New("source unreachable")},
		// The source hands back the same address, which is it saying the link is
		// not the problem. Retrying it would be a second identical failure.
		"same address": &fakeResolver{url: dead.URL},
	} {
		t.Run(name, func(t *testing.T) {
			withResolver(t, r)
			s := newTestSealer(t)
			raw, err := s.Mint(dead.URL, nil, "session-1", Plan{DirectPlay: true}, For("part-7", "browser"))
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			rec := httptest.NewRecorder()
			Handler(s, http.DefaultClient, NewRemuxerAt("")).
				ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))
			if rec.Code == http.StatusOK {
				t.Errorf("status = 200, want the upstream's own failure relayed")
			}
		})
	}

	// And a ticket that carries no part or class — minted before this existed,
	// or by a path that has nothing to re-ask for — behaves exactly as it did
	// before, with one attempt and no resolver call.
	fake := &fakeResolver{url: "http://unused.invalid"}
	withResolver(t, fake)
	s := newTestSealer(t)
	raw, err := s.Mint(dead.URL, nil, "session-1", Plan{DirectPlay: true})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt("")).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))
	if fake.calls != 0 {
		t.Errorf("re-resolved %d times for a ticket that names no release", fake.calls)
	}
}

// TestNoResolverIsTheOldBehaviour pins the degradation. A Platform assembled
// without one — every test that does not install one, and any future
// composition that omits it — must still serve, not panic.
func TestNoResolverIsTheOldBehaviour(t *testing.T) {
	withResolver(t, nil)
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "gone", http.StatusNotFound)
	}))
	defer dead.Close()

	s := newTestSealer(t)
	raw, err := s.Mint(dead.URL, nil, "session-1", Plan{DirectPlay: true}, For("part-7", "browser"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt("")).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw, nil))
	if rec.Code == http.StatusOK {
		t.Error("a dead link with no resolver must still fail honestly")
	}
}

// TestTheTicketCarriesTheReleaseAndClass is the seal itself. Both ride encrypted
// inside the ticket rather than in the URL, so a client cannot name a different
// release for the origin to resolve on its behalf.
func TestTheTicketCarriesTheReleaseAndClass(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/x.mkv", nil, "session-1", Plan{}, For("part-7", "tv"))
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if got, err := s.open(raw); err != nil {
		t.Fatalf("open: %v", err)
	} else if got.PartID != "part-7" || got.Class != "tv" {
		t.Errorf("ticket carries %q/%q, want part-7/tv", got.PartID, got.Class)
	}
	for _, leaked := range []string{"part-7", "tv"} {
		if contains(raw, leaked) {
			t.Errorf("the ticket string leaks %q in the clear", leaked)
		}
	}
}

func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}
