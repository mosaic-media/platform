// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// Module-found subtitle files (platform#83). Unlike every other track here they
// are not in the release, which is what lets a direct-played stream have them.

// TestAnExternalSubtitleIsServedOnBothPaths is the property that makes this
// worth having. An embedded track needs a playlist to hang a rendition off, so a
// direct-played release cannot have one — a file from elsewhere has no such
// constraint, and refusing it on the relayed path would throw that away.
func TestAnExternalSubtitleIsServedOnBothPaths(t *testing.T) {
	for name, plan := range map[string]Plan{
		"direct play": {DirectPlay: true, External: []ExternalSubtitle{{URL: "https://subs.example/a.srt"}}},
		"transcoded":  {Duration: seekablePlan().Duration, External: []ExternalSubtitle{{URL: "https://subs.example/a.srt"}}},
	} {
		t.Run(name, func(t *testing.T) {
			s := newTestSealer(t)
			raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
			if err != nil {
				t.Fatalf("Mint: %v", err)
			}
			rec := httptest.NewRecorder()
			Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "WEBVTT\n\n00:01.000 --> 00:02.000\nhello\n"))).
				ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+ExternalSubtitleName(0), nil))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200\n%s", rec.Code, rec.Body.String())
			}
			if ct := rec.Header().Get("Content-Type"); ct != vttMimeType {
				t.Errorf("Content-Type = %q, want %q", ct, vttMimeType)
			}
			if !strings.HasPrefix(rec.Body.String(), "WEBVTT") {
				t.Errorf("body = %q, want WebVTT", rec.Body.String())
			}
		})
	}
}

// TestTheModuleURLNeverReachesTheClient is the platform#25 property: a module
// resolves and the Platform serves. The URL may carry a credential, and pointing
// a browser at it would also hand a third party the viewer's address.
func TestTheModuleURLNeverReachesTheClient(t *testing.T) {
	const secret = "https://subs.example/a.srt?token=abc123"
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{
		DirectPlay: true,
		External:   []ExternalSubtitle{{URL: secret, Language: "eng", Label: "English — opensubs"}},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	if strings.Contains(raw, "subs.example") || strings.Contains(raw, "abc123") {
		t.Error("the ticket string leaks the module's URL in the clear")
	}
	got, err := s.open(raw)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if len(got.Plan.External) != 1 || got.Plan.External[0].URL != secret {
		t.Errorf("sealed external = %+v, want the URL carried intact", got.Plan.External)
	}
}

// TestAnUnofferedExternalSubtitleIsNotFound closes the surface. The index comes
// from a URL and selects into a list carried in the ticket, so anything outside
// it must be refused rather than clamped — otherwise the resource is a way to
// ask the origin to fetch an arbitrary thing.
func TestAnUnofferedExternalSubtitleIsNotFound(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{
		DirectPlay: true, External: []ExternalSubtitle{{URL: "https://subs.example/a.srt"}},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	h := Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "WEBVTT\n")))

	for _, resource := range []string{
		ExternalSubtitleName(1), ExternalSubtitleName(99),
		"ext.vtt", "ext-1.vtt", "ext01.vtt", "extx.vtt",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+resource, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", resource, rec.Code)
		}
	}
}

// TestAnEmptyFetchIsAFailureNotAnEmptyTrack pins the honest failure. A fetch
// that died and a file that is genuinely empty are indistinguishable once a 200
// has gone out, and the client would then list a subtitle track that draws
// nothing rather than falling back to what the release carried.
func TestAnEmptyFetchIsAFailureNotAnEmptyTrack(t *testing.T) {
	s := newTestSealer(t)
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", Plan{
		DirectPlay: true, External: []ExternalSubtitle{{URL: "https://subs.example/gone.srt"}},
	})
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, ""))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+ExternalSubtitleName(0), nil))

	if rec.Code == http.StatusOK {
		t.Errorf("status = 200 for a fetch that produced nothing; the client would show an empty track")
	}
}
