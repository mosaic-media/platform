// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package clientaddr_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mosaic-media/platform/internal/transport/clientaddr"
)

// resolved runs a request through the middleware and reports what it attributed.
func resolved(t *testing.T, trust bool, remoteAddr string, forwarded ...string) string {
	t.Helper()
	var got string
	handler := clientaddr.Middleware(trust)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = clientaddr.From(r.Context())
	}))

	req := httptest.NewRequest(http.MethodPost, "/mosaic.auth.v1.AuthService/Bootstrap", nil)
	req.RemoteAddr = remoteAddr
	for _, v := range forwarded {
		req.Header.Add("X-Forwarded-For", v)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return got
}

// TestBehindTheFrontDoorTheForwardedAddressWins pins the reason this package
// exists: behind the front door the connection's peer is the Supervisor, so the
// caller has to come from the header.
func TestBehindTheFrontDoorTheForwardedAddressWins(t *testing.T) {
	if got := resolved(t, true, "@", "203.0.113.7"); got != "203.0.113.7" {
		t.Errorf("attributed to %q, want the forwarded caller", got)
	}
}

// TestAForgedForwardedPrefixIsIgnored pins which entry is believed. Everything
// left of the last one was supplied by the caller: a client that sends its own
// X-Forwarded-For has the front door append the address it observed after it.
// Taking the leftmost value would let any caller choose its own rate-limit
// bucket, and a different one per request — not a weaker limit but no limit at
// all.
func TestAForgedForwardedPrefixIsIgnored(t *testing.T) {
	// The client claimed 1.2.3.4; the front door appended what it really saw.
	got := resolved(t, true, "@", "1.2.3.4, 198.51.100.9")
	if got == "1.2.3.4" {
		t.Fatal("attributed to the address the caller claimed — the limit is opt-out")
	}
	if got != "198.51.100.9" {
		t.Errorf("attributed to %q, want the front door's own observation", got)
	}
}

// TestAForgedSeparateHeaderLineIsIgnored pins the flattening. A caller can send
// X-Forwarded-For as its own header line rather than in the proxy's, which
// arrives as two values rather than one comma-joined one; flattening before
// taking the last is what makes those the same case.
func TestAForgedSeparateHeaderLineIsIgnored(t *testing.T) {
	if got := resolved(t, true, "@", "1.2.3.4", "198.51.100.9"); got != "198.51.100.9" {
		t.Errorf("attributed to %q, want the last entry across all header lines", got)
	}
}

// TestOnAnUntrustedListenerTheHeaderIsIgnored pins that on a listener anybody
// can connect to, the header is a claim by whoever connected and is ignored
// entirely.
func TestOnAnUntrustedListenerTheHeaderIsIgnored(t *testing.T) {
	if got := resolved(t, false, "198.51.100.9:5000", "1.2.3.4"); got != "198.51.100.9" {
		t.Errorf("attributed to %q, want the observed peer", got)
	}
}

// TestThePortIsStripped pins that ports are stripped, so one caller's many
// connections share one bucket rather than getting a fresh one per ephemeral
// port.
func TestThePortIsStripped(t *testing.T) {
	if got := resolved(t, false, "198.51.100.9:41234"); got != "198.51.100.9" {
		t.Errorf("got %q", got)
	}
	if got := resolved(t, true, "@", "[2001:db8::1]:443"); got != "2001:db8::1" {
		t.Errorf("IPv6 with a port: got %q", got)
	}
	if got := resolved(t, true, "@", "2001:db8::1"); got != "2001:db8::1" {
		t.Errorf("bare IPv6: got %q", got)
	}
}

// TestNoAddressAtAllIsOneSharedBucketNotAnEmptyKey pins that a trusted listener
// with no header does not produce an empty key. A Unix socket has no peer
// address, and an empty string per caller would be one bucket shared by
// everyone.
func TestNoAddressAtAllIsOneSharedBucketNotAnEmptyKey(t *testing.T) {
	if got := resolved(t, true, "@"); got != clientaddr.Unknown {
		t.Errorf("got %q, want %q", got, clientaddr.Unknown)
	}
	if got := resolved(t, true, ""); got != clientaddr.Unknown {
		t.Errorf("got %q, want %q", got, clientaddr.Unknown)
	}
}

// TestAContextThatNeverPassedThroughResolvesToUnknown pins that a path which
// never passed through the middleware — an in-process call in a test — still
// gets a usable key rather than an empty one.
func TestAContextThatNeverPassedThroughResolvesToUnknown(t *testing.T) {
	if got := clientaddr.From(httptest.NewRequest(http.MethodGet, "/", nil).Context()); got != clientaddr.Unknown {
		t.Errorf("got %q, want %q", got, clientaddr.Unknown)
	}
}

// TestANonAddressIsNotTreatedAsOne pins that anything which is not an IP is not
// an address. A Unix connection reports its peer as "@", identical for every
// caller, so accepting it would be one shared bucket disguised as a resolved
// address.
func TestANonAddressIsNotTreatedAsOne(t *testing.T) {
	for _, notAnAddress := range []string{"@", "/run/mosaic/platform.sock", "localhost", "not-an-ip"} {
		if got := resolved(t, false, notAnAddress); got != clientaddr.Unknown {
			t.Errorf("%q resolved to %q, want %q", notAnAddress, got, clientaddr.Unknown)
		}
	}
	// And the same in a forwarded header, where a caller controls the text.
	if got := resolved(t, true, "@", "not-an-ip"); got != clientaddr.Unknown {
		t.Errorf("a non-address in the header resolved to %q", got)
	}
}

// TestEmptyAndPaddedEntriesAreSkipped pins that whitespace and empty entries —
// what a hand-written header looks like — are skipped, so an empty last entry
// does not become the key.
func TestEmptyAndPaddedEntriesAreSkipped(t *testing.T) {
	if got := resolved(t, true, "@", " 1.2.3.4 ,  198.51.100.9  , "); got != "198.51.100.9" {
		t.Errorf("got %q", got)
	}
}
