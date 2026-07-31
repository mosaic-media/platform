// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package playback

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The delivery half of ADR 0112, which is where the escalation stops being a
// computed value and becomes something on a screen (ADR 0113).

func offered(tracks []SubtitleTrack, intent SubtitleIntent) []SubtitleDelivery {
	return DecideSubtitles(tracks, intent)
}

// defaultedTo reports the label of the track marked default, or "" for none.
func defaultedTo(out []SubtitleDelivery) string {
	for _, s := range out {
		if s.Default {
			return s.Label
		}
	}
	return ""
}

// TestAdamsTwoReleasesEndUpWithDifferentTracks is the pair of cases ADR 0112 was
// written from, carried through to the rendition that actually reaches a player.
// The preference is identical in both; the release is what differs.
func TestAdamsTwoReleasesEndUpWithDifferentTracks(t *testing.T) {
	tracks := []SubtitleTrack{
		{Index: 3, Language: "eng", Forced: true},
		{Index: 4, Language: "eng"},
	}
	adam := LanguagePreference{Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesForced}

	// With an English dub he hears English, so the forced track — signage only —
	// is what comes on.
	withDub := offered(tracks, adam.SubtitlesFor("eng"))
	if got := defaultedTo(withDub); got != "English (forced)" {
		t.Errorf("with a dub the default is %q, want the forced English track", got)
	}

	// Without one he is reading dialogue he cannot hear, and the escalation
	// reaches all the way to which rendition is on.
	noDub := offered(tracks, adam.SubtitlesFor("jpn"))
	if got := defaultedTo(noDub); got != "English" {
		t.Errorf("with no dub the default is %q, want the full English track", got)
	}

	// Either way both tracks are listed. The preference decides what comes on,
	// never what a viewer is allowed to reach.
	if len(withDub) != 2 || len(noDub) != 2 {
		t.Errorf("offered %d and %d tracks, want both listed in both cases", len(withDub), len(noDub))
	}
}

// TestOffOffersEverythingAndTurnsNothingOn is the distinction the whole file
// rests on. Someone who wants no subtitles gets none — and still gets the menu,
// because turning them on for one scene is not a change of preference.
func TestOffOffersEverythingAndTurnsNothingOn(t *testing.T) {
	tracks := []SubtitleTrack{{Index: 3, Language: "eng"}, {Index: 4, Language: "jpn"}}
	off := LanguagePreference{Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesOff}

	out := offered(tracks, off.SubtitlesFor("jpn"))
	if len(out) != 2 {
		t.Fatalf("offered %d tracks, want both — off is a default, not a censor", len(out))
	}
	if got := defaultedTo(out); got != "" {
		t.Errorf("default = %q, want nothing on", got)
	}
}

// TestALanguageNobodyAskedForIsNeverTurnedOn is ADR 0112's rule stated as a
// property. A subtitle track somebody cannot read occupies the screen and
// communicates nothing, which is worse than no subtitles at all.
func TestALanguageNobodyAskedForIsNeverTurnedOn(t *testing.T) {
	tracks := []SubtitleTrack{{Index: 3, Language: "swe"}, {Index: 4, Language: "dan"}}
	adam := LanguagePreference{Audio: []string{"eng"}, Subtitles: []string{"eng"}, SubtitleMode: SubtitlesFull}

	out := offered(tracks, adam.SubtitlesFor("jpn"))
	if got := defaultedTo(out); got != "" {
		t.Errorf("default = %q, want nothing — neither track is in a language he asked for", got)
	}
	if len(out) != 2 {
		t.Errorf("offered %d tracks, want both listed so he can still choose one", len(out))
	}
}

// TestTheModeTakesWhatTheReleaseHas covers the case a strict reading would get
// wrong: a viewer asking for forced subtitles on a release that ships none. The
// full track is more than silence and is what the release actually has.
func TestTheModeTakesWhatTheReleaseHas(t *testing.T) {
	forcedOnly := []SubtitleTrack{{Index: 3, Language: "eng", Forced: true}}
	fullOnly := []SubtitleTrack{{Index: 3, Language: "eng"}}

	wantsForced := SubtitleIntent{Mode: SubtitlesForced, Languages: []string{"eng"}}
	if got := defaultedTo(offered(fullOnly, wantsForced)); got != "English" {
		t.Errorf("forced wanted, only full present: default = %q, want the full track", got)
	}

	wantsFull := SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng"}}
	if got := defaultedTo(offered(forcedOnly, wantsFull)); got != "English (forced)" {
		t.Errorf("full wanted, only forced present: default = %q, want the forced track", got)
	}
}

// TestTheViewersOrderWinsOverTheContainers guards a silent wrong answer. A
// release routinely carries its tracks in whatever order it was built in, and
// ranking by that rather than by the preference gives someone their second
// language whenever it happens to be listed first.
func TestTheViewersOrderWinsOverTheContainers(t *testing.T) {
	tracks := []SubtitleTrack{{Index: 3, Language: "spa"}, {Index: 4, Language: "eng"}}
	intent := SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng", "spa"}}

	if got := defaultedTo(offered(tracks, intent)); got != "English" {
		t.Errorf("default = %q, want English — it is first in the preference, not in the file", got)
	}
}

// TestAtMostOneTrackIsEverDefault is a property rather than a case. Two default
// renditions in a master is a playlist a player is entitled to resolve either
// way, so the bug would present as subtitles that are correct on one client and
// wrong on another.
func TestAtMostOneTrackIsEverDefault(t *testing.T) {
	tracks := []SubtitleTrack{
		{Index: 3, Language: "eng", Forced: true},
		{Index: 4, Language: "eng"},
		{Index: 5, Language: "eng"},
		{Index: 6, Language: "spa"},
	}
	for _, mode := range []SubtitleMode{SubtitlesOff, SubtitlesForced, SubtitlesFull} {
		out := offered(tracks, SubtitleIntent{Mode: mode, Languages: []string{"eng", "spa"}})
		count := 0
		for _, s := range out {
			if s.Default {
				count++
			}
		}
		if count > 1 {
			t.Errorf("mode %q marked %d tracks default, want at most one", mode, count)
		}
	}
}

// TestASourceIndexTravelsRatherThanAPosition guards the mapping the extraction
// depends on. The rendition list is renumbered from zero; the stream it maps is
// numbered by the container, and a release whose subtitles start at stream 7
// would otherwise extract its video.
func TestASourceIndexTravelsRatherThanAPosition(t *testing.T) {
	out := offered([]SubtitleTrack{{Index: 7, Language: "eng"}, {Index: 9, Language: "spa"}},
		SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng"}})

	if len(out) != 2 || out[0].Index != 7 || out[1].Index != 9 {
		t.Fatalf("offered %+v, want the source's own stream indexes preserved", out)
	}
}

// TestTheMasterDeclaresWhatThePlayerNeedsToKnow checks the one artefact a client
// actually reads. Every claim here is one a player acts on, and getting any of
// them wrong presents as subtitles that silently do not appear.
func TestTheMasterDeclaresWhatThePlayerNeedsToKnow(t *testing.T) {
	subs := offered([]SubtitleTrack{
		{Index: 3, Language: "eng", Forced: true},
		{Index: 4, Language: "jpn"},
	}, SubtitleIntent{Mode: SubtitlesForced, Languages: []string{"eng"}})

	got := masterPlaylist(masterBandwidth(4<<30, 2*time.Hour), subs)

	for _, want := range []string{
		"#EXTM3U",
		`TYPE=SUBTITLES,GROUP-ID="subs"`,
		`NAME="English (forced)"`,
		`LANGUAGE="eng"`,
		"DEFAULT=YES",
		"FORCED=YES",
		`URI="s0.m3u8"`,
		`URI="s1.m3u8"`,
		`SUBTITLES="subs"`,
		videoPlaylistName,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("master playlist is missing %q:\n%s", want, got)
		}
	}

	// The Japanese track is listed and off, which is the same rule the decision
	// makes, checked where a player would read it.
	if strings.Count(got, "DEFAULT=YES") != 1 {
		t.Errorf("want exactly one DEFAULT=YES:\n%s", got)
	}
	// CODECS stays out. A wrong one makes a browser refuse a stream it could
	// have played, and the origin cannot know it before the transcode runs.
	if strings.Contains(got, "CODECS") {
		t.Errorf("master declares CODECS, which the origin cannot know:\n%s", got)
	}
	// BANDWIDTH is required by the grammar even though there is nothing to
	// choose between.
	if !strings.Contains(got, "BANDWIDTH=") {
		t.Errorf("master has no BANDWIDTH, which the grammar requires:\n%s", got)
	}
}

// TestTheSubtitleRenditionCoversTheWholeRelease is the same property the video
// playlist has and for the same reason: a player must be able to seek to the end
// of a film before anything has been produced.
func TestTheSubtitleRenditionCoversTheWholeRelease(t *testing.T) {
	const total = 95 * time.Minute
	got := subtitlePlaylist(total, 0)

	if !strings.Contains(got, "#EXT-X-ENDLIST") {
		t.Errorf("no ENDLIST, so a player treats the tail as not yet existing:\n%s", got)
	}
	want := segmentCount(total, subtitleWindow)
	if n := strings.Count(got, ".vtt"); n != want {
		t.Errorf("%d segments for %v, want %d", n, total, want)
	}
	// The last window is short, and it is still a window — dropping it would
	// lose the closing minutes of dialogue of every film that does not divide
	// evenly.
	if !strings.Contains(got, subtitleSegmentName(0, want-1)) {
		t.Errorf("the final short window is missing:\n%s", got)
	}
	// No EXT-X-MAP: a WebVTT segment is a complete document on its own.
	if strings.Contains(got, "EXT-X-MAP") {
		t.Errorf("subtitle rendition declares an init segment it does not have:\n%s", got)
	}
}

// TestSubtitleResourceNamesRoundTrip is the parsing half. These names arrive in
// a URL, so the only safe reading is an exact match against what the playlist
// emitted — anything looser is a path from a stranger.
func TestSubtitleResourceNamesRoundTrip(t *testing.T) {
	for _, c := range []struct{ track, segment int }{{0, 0}, {1, 7}, {12, 340}} {
		name := subtitleSegmentName(c.track, c.segment)
		track, segment, ok := subtitleSegmentOf(name)
		if !ok || track != c.track || segment != c.segment {
			t.Errorf("subtitleSegmentOf(%q) = %d,%d,%v; want %d,%d,true", name, track, segment, ok, c.track, c.segment)
		}
		if i, ok := subtitlePlaylistOf(subtitlePlaylistName(c.track)); !ok || i != c.track {
			t.Errorf("subtitlePlaylistOf round trip for %d = %d,%v", c.track, i, ok)
		}
	}

	for _, bad := range []string{
		"", "s.vtt", "s0.vtt", "s0_.vtt", "s_1.vtt", "sx_1.vtt", "s0_x.vtt",
		"s0_01.vtt", "s01_1.vtt", "s-1_0.vtt", "s0_-1.vtt",
		"../s0_0.vtt", "s0_0.vtt.bak", "0.m4s",
	} {
		if _, _, ok := subtitleSegmentOf(bad); ok {
			t.Errorf("subtitleSegmentOf(%q) accepted a name the playlist never emits", bad)
		}
	}
	for _, bad := range []string{"", "s.m3u8", "index.m3u8", "sx.m3u8", "s01.m3u8", "v.m3u8"} {
		if _, ok := subtitlePlaylistOf(bad); ok {
			t.Errorf("subtitlePlaylistOf(%q) accepted a name the master never emits", bad)
		}
	}
}

// TestNoSubtitlesMeansNoMaster is what keeps this slice from costing anything to
// a release that has none: the entry point is the same media playlist it always
// was, and no extra round trip was bought for an empty rendition list.
func TestNoSubtitlesMeansNoMaster(t *testing.T) {
	if got := DecideSubtitles(nil, SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng"}}); got != nil {
		t.Errorf("DecideSubtitles(nil) = %+v, want nothing to declare", got)
	}
}

// TestTheOriginServesTheWholeRenditionChain walks what a player actually does:
// fetch the entry point, follow it to the subtitle rendition, follow that to a
// segment. Every hop is a URL the previous document emitted, so a break anywhere
// in the chain presents as subtitles that silently never appear.
func TestTheOriginServesTheWholeRenditionChain(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	s := newTestSealer(t)
	plan := seekablePlan()
	plan.Subtitles = offered([]SubtitleTrack{{Index: 3, Language: "eng", Forced: true}},
		SubtitleIntent{Mode: SubtitlesForced, Languages: []string{"eng"}})
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	h := Handler(s, http.DefaultClient, NewRemuxerAt(recordingFFmpeg(t, "WEBVTT\n", argsFile)))

	get := func(resource string) *httptest.ResponseRecorder {
		t.Helper()
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+resource, nil))
		return rec
	}

	// 1. The entry point is now a master, because there is a rendition to declare.
	master := get(PlaylistName)
	if master.Code != http.StatusOK || !strings.Contains(master.Body.String(), "EXT-X-MEDIA:TYPE=SUBTITLES") {
		t.Fatalf("entry point is not a master: %d\n%s", master.Code, master.Body.String())
	}

	// 2. The video it names still resolves, which is the hop that would break if
	//    the master moved the media playlist and nothing served it at its new name.
	video := get(videoPlaylistName)
	if video.Code != http.StatusOK || !strings.Contains(video.Body.String(), ".m4s") {
		t.Fatalf("video rendition at %s: %d\n%s", videoPlaylistName, video.Code, video.Body.String())
	}

	// 3. The subtitle rendition the master named.
	rendition := get(subtitlePlaylistName(0))
	if rendition.Code != http.StatusOK || !strings.Contains(rendition.Body.String(), subtitleSegmentName(0, 0)) {
		t.Fatalf("subtitle rendition: %d\n%s", rendition.Code, rendition.Body.String())
	}

	// 4. A segment of it, served as WebVTT.
	seg := get(subtitleSegmentName(0, 3))
	if seg.Code != http.StatusOK {
		t.Fatalf("subtitle segment: %d\n%s", seg.Code, seg.Body.String())
	}
	if ct := seg.Header().Get("Content-Type"); ct != vttMimeType {
		t.Errorf("segment Content-Type = %q, want %q — a player will not parse it otherwise", ct, vttMimeType)
	}

	// And the extraction asked for the right window of the right stream.
	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ffmpeg was never started: %v", err)
	}
	line := string(got)
	for _, want := range []string{"-ss 180.000", "-copyts", "-to 240.000", "-map 0:3", "-c:s webvtt"} {
		if !strings.Contains(line, want) {
			t.Errorf("extraction args %q lack %q", line, want)
		}
	}
}

// TestTheSubtitleWindowIsBoundedAbsolutelyNotByDuration pins a measured bug
// rather than a style preference.
//
// `-t` bounds the output's duration, and `-copyts` has already rebased the
// output onto the source's clock — so `-ss 60 ... -t 60` stops the instant it
// starts. Measured against a file with a cue every ten seconds: window 0 yielded
// six cues and window 1 yielded none, which as a bug would be every subtitle in
// a film disappearing one minute in.
func TestTheSubtitleWindowIsBoundedAbsolutelyNotByDuration(t *testing.T) {
	argsFile := filepath.Join(t.TempDir(), "args")
	s := newTestSealer(t)
	plan := seekablePlan()
	plan.Subtitles = offered([]SubtitleTrack{{Index: 2, Language: "eng"}},
		SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng"}})
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}

	rec := httptest.NewRecorder()
	Handler(s, http.DefaultClient, NewRemuxerAt(recordingFFmpeg(t, "WEBVTT\n", argsFile))).
		ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+subtitleSegmentName(0, 5), nil))

	got, err := os.ReadFile(argsFile)
	if err != nil {
		t.Fatalf("ffmpeg was never started: %v", err)
	}
	line := " " + string(got)
	if !strings.Contains(line, " -to 360.000") {
		t.Errorf("args %q do not bound the window absolutely; window 5 must end at 360s", line)
	}
	if strings.Contains(line, " -t ") {
		t.Errorf("args %q use -t, which with -copyts empties every window but the first", line)
	}
}

// TestAnUnknownSubtitleResourceIsNotFound closes the surface. These names arrive
// in a URL and index into a list carried in the ticket, so anything the playlist
// did not emit must be refused rather than clamped.
func TestAnUnknownSubtitleResourceIsNotFound(t *testing.T) {
	s := newTestSealer(t)
	plan := seekablePlan()
	plan.Subtitles = offered([]SubtitleTrack{{Index: 3, Language: "eng"}},
		SubtitleIntent{Mode: SubtitlesFull, Languages: []string{"eng"}})
	raw, err := s.Mint("https://cdn.example/movie.mkv", nil, "session-1", plan)
	if err != nil {
		t.Fatalf("Mint: %v", err)
	}
	h := Handler(s, http.DefaultClient, NewRemuxerAt(fakeFFmpeg(t, "WEBVTT\n")))

	for _, resource := range []string{
		// A track that was never offered.
		subtitlePlaylistName(1), subtitleSegmentName(1, 0),
		// A window past the end of a two-hour release.
		subtitleSegmentName(0, 120),
		// Names no playlist emits.
		"s.m3u8", "s0_x.vtt", "master.m3u8",
	} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/playback/"+raw+"/"+resource, nil))
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want 404", resource, rec.Code)
		}
	}
}
