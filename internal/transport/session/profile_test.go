// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	sessionv1 "github.com/mosaic-media/sdui/gen/mosaic/session/v1"
)

// TestCapabilityClassIsOrderIndependent is the property the resolution cache
// rests on (ADR 0049). Two clients with identical abilities must land in one
// class however they happened to list them — a client that enumerates its codecs
// from a Set, or in whatever order `canPlayType` was probed, does not get a cache
// of its own. The failure this guards against is silent: a fragmented cache
// still returns correct answers, it just never seems to be warm.
func TestCapabilityClassIsOrderIndependent(t *testing.T) {
	a := profileFrom(&sessionv1.ClientProfile{
		VideoCodecs: []string{"h264", "hevc", "av1"},
		AudioCodecs: []string{"aac", "opus"},
		MaxHeight:   1080,
	})
	b := profileFrom(&sessionv1.ClientProfile{
		VideoCodecs: []string{"av1", "H264", "HEVC"},
		AudioCodecs: []string{"opus", "aac"},
		MaxHeight:   1080,
	})
	if a.class != b.class {
		t.Fatalf("same abilities produced different classes: %q vs %q", a.class, b.class)
	}
}

// TestCapabilityClassSeparatesRealDifferences is the other half: a class that
// collapsed genuinely different clients would serve one an answer chosen for the
// other, which is how a phone ends up handed 4K HDR.
func TestCapabilityClassSeparatesRealDifferences(t *testing.T) {
	base := &sessionv1.ClientProfile{
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac"},
		MaxHeight:   1080,
	}
	seen := map[string]string{profileFrom(base).class: "base"}

	variants := map[string]*sessionv1.ClientProfile{
		"extra video codec": {VideoCodecs: []string{"h264", "hevc"}, AudioCodecs: []string{"aac"}, MaxHeight: 1080},
		"extra audio codec": {VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac", "eac3"}, MaxHeight: 1080},
		"hdr":               {VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 1080, Hdr: true},
		"taller":            {VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 2160},
		"container stated":  {Containers: []string{"mp4"}, VideoCodecs: []string{"h264"}, AudioCodecs: []string{"aac"}, MaxHeight: 1080},
	}
	for name, p := range variants {
		class := profileFrom(p).class
		if other, clash := seen[class]; clash {
			t.Errorf("%q shares a class with %q (%s)", name, other, class)
			continue
		}
		seen[class] = name
	}
}

// TestUndeclaredProfileFallsBackToTheBrowserAssumption proves an older client
// keeps working. The proto field is optional on purpose, and the answer for a
// client that says nothing is the assumption the Platform used to hard-code for
// everybody — not an empty preference, which would rank on nothing at all.
func TestUndeclaredProfileFallsBackToTheBrowserAssumption(t *testing.T) {
	s := newLiveSession("ref", time.Now())
	got := s.clientProfile()

	if got.declared {
		t.Fatal("a session that was never told a profile reports one as declared")
	}
	if got.class != legacyBrowserClass {
		t.Errorf("class = %q, want %q", got.class, legacyBrowserClass)
	}
	if got.prefer.Empty() {
		t.Fatal("fallback preference is empty; selection would rank on nothing")
	}
	if !got.prefer.VideoCodecs["h264"] || got.prefer.AudioCodecs["eac3"] {
		t.Errorf("fallback is not the browser assumption: %+v", got.prefer)
	}
	// And the guess must never share a cache entry with a client that declared
	// the same thing: only one of the two is known to be true.
	declared := profileFrom(&sessionv1.ClientProfile{
		VideoCodecs: []string{"h264", "hevc", "vp9", "av1", "vp8"},
		AudioCodecs: []string{"aac", "mp3", "opus", "vorbis", "flac"},
	})
	if declared.class == got.class {
		t.Error("a declared profile collided with the undeclared fallback class")
	}
}

// TestDeclaredProfileIsWhatSelectionRanksOn walks the path the roadmap called
// out: the hard-coded browser preference at the playPart call site is gone, and
// what a client declared on Attach is what reaches selection.
func TestDeclaredProfileIsWhatSelectionRanksOn(t *testing.T) {
	s := newLiveSession("ref", time.Now())
	s.setProfile(profileFrom(&sessionv1.ClientProfile{
		Containers:  []string{"mp4"},
		VideoCodecs: []string{"h264"},
		AudioCodecs: []string{"aac", "eac3"},
		Hdr:         true,
		MaxHeight:   2160,
	}))

	got := s.clientProfile()
	if !got.declared {
		t.Fatal("declared profile did not survive the round trip")
	}
	// A TV that decodes E-AC3 must not be ranked with the browser's assumption
	// that nothing does — that single fact decides whether a release has sound.
	if !got.prefer.AudioCodecs["eac3"] {
		t.Error("declared eac3 support was lost")
	}
	if got.prefer.VideoCodecs["hevc"] {
		t.Error("undeclared hevc support was invented")
	}
	if !got.prefer.HDR || got.prefer.MaxHeight != 2160 || !got.prefer.Containers["mp4"] {
		t.Errorf("declared profile mangled: %+v", got.prefer)
	}
}

// TestEncodeHeightIsCappedForAnUncappedClient records a deliberate asymmetry: a
// client may honestly report no display ceiling, and the encoder still gets one.
// The two answer different questions, and the software encoder on a home server
// is the smaller number every time.
func TestEncodeHeightIsCappedForAnUncappedClient(t *testing.T) {
	uncapped := profileFrom(&sessionv1.ClientProfile{VideoCodecs: []string{"h264"}})
	if got := uncapped.codecs().MaxHeight; got != defaultEncodeHeight {
		t.Errorf("encode height = %d, want the default cap %d", got, defaultEncodeHeight)
	}
	if uncapped.prefer.MaxHeight != 0 {
		t.Errorf("selection height = %d, want 0: the client capped nothing", uncapped.prefer.MaxHeight)
	}

	capped := profileFrom(&sessionv1.ClientProfile{VideoCodecs: []string{"h264"}, MaxHeight: 720})
	if got := capped.codecs().MaxHeight; got != 720 {
		t.Errorf("encode height = %d, want the declared 720", got)
	}
}

// TestCodecsCarryTheDeclaredHDRAnswer ties the declaration to the per-stream
// decision (ADR 0050). Selection and planning must read the same profile: a
// client picked for its codecs and then planned against a different one gets a
// re-encode of the very release that was chosen to avoid one.
func TestCodecsCarryTheDeclaredHDRAnswer(t *testing.T) {
	hdr := profileFrom(&sessionv1.ClientProfile{VideoCodecs: []string{"hevc"}, Hdr: true})
	if !hdr.codecs().HDR {
		t.Error("declared HDR support did not reach the decision")
	}
	if !hdr.codecs().Video["hevc"] {
		t.Error("declared video codecs did not reach the decision")
	}

	sdr := profileFrom(&sessionv1.ClientProfile{VideoCodecs: []string{"hevc"}})
	if sdr.codecs().HDR {
		t.Error("HDR was assumed for a client that did not claim it")
	}
}

// TestCodecSetNormalises proves the lowercasing that lets a client shouting
// "HEVC" match a probe reporting "hevc", and that blanks are dropped rather than
// becoming a codec named "".
func TestCodecSetNormalises(t *testing.T) {
	got := codecSet([]string{"HEVC", " aac ", "", "   "})
	if len(got) != 2 || !got["hevc"] || !got["aac"] {
		t.Errorf("codecSet = %v, want {hevc, aac}", got)
	}
	if codecSet(nil) != nil {
		t.Error("an empty declaration should stay nil, not become an empty set")
	}
}

// TestCapabilityClassIsStableAcrossRuns guards the one property a digest can
// lose without anything failing: it must not depend on map iteration order. Ten
// derivations of the same profile is enough to shake that out.
func TestCapabilityClassIsStableAcrossRuns(t *testing.T) {
	p := app.PlaybackPreference{
		Containers:  map[string]bool{"mp4": true, "webm": true, "mkv": true},
		VideoCodecs: map[string]bool{"h264": true, "hevc": true, "av1": true, "vp9": true},
		AudioCodecs: map[string]bool{"aac": true, "opus": true, "flac": true},
		MaxHeight:   1080,
	}
	want := capabilityClass(p)
	for i := 0; i < 10; i++ {
		if got := capabilityClass(p); got != want {
			t.Fatalf("class is not stable: %q then %q", want, got)
		}
	}
}
