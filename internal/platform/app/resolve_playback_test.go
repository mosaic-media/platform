// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// browser is what a desktop browser can decode: HEVC yes (a live test proved
// Chrome plays it), E-AC3 no (Chrome plays it nowhere).
var browser = PlaybackPreference{
	VideoCodecs: map[string]bool{"h264": true, "hevc": true, "av1": true},
	AudioCodecs: map[string]bool{"aac": true, "opus": true, "mp3": true},
}

// The candidate set that motivated selection: the source ranked a 4K HEVC/E-AC3
// release first, and a browser given that one plays it silently.
var (
	fourKNoSound = v1.Part{
		ID: "p-4k", NaturalOrder: 0, EditionLabel: "Thor.2011.2160p.HDR.HEVC.EAC3",
		VideoCodec: "hevc", AudioCodec: "eac3", Height: 2160, Container: "mkv",
	}
	tenEightyPlayable = v1.Part{
		ID: "p-1080", NaturalOrder: 7, EditionLabel: "Thor.2011.1080p.x264.AAC",
		VideoCodec: "h264", AudioCodec: "aac", Height: 1080, Container: "mp4",
	}
	sevenTwentyPlayable = v1.Part{
		ID: "p-720", NaturalOrder: 12, EditionLabel: "Thor.2011.720p.x264.AAC",
		VideoCodec: "h264", AudioCodec: "aac", Height: 720, Container: "mp4",
	}
)

// TestSelectionPrefersPlayableOverHigherResolution is ADR 0048's argument in one
// assertion: an unplayable 4K release is worth less than a playable 1080p one,
// so compatibility has to outweigh resolution rather than merely tie-break it.
func TestSelectionPrefersPlayableOverHigherResolution(t *testing.T) {
	if playbackScore(tenEightyPlayable, browser) <= playbackScore(fourKNoSound, browser) {
		t.Fatalf("1080p playable (%d) must outrank 4K with undecodable audio (%d)",
			playbackScore(tenEightyPlayable, browser), playbackScore(fourKNoSound, browser))
	}
}

// TestSelectionPrefersHigherResolutionAmongPlayables — once compatibility is
// settled, resolution decides, or every film would play at 480p.
func TestSelectionPrefersHigherResolutionAmongPlayables(t *testing.T) {
	if playbackScore(tenEightyPlayable, browser) <= playbackScore(sevenTwentyPlayable, browser) {
		t.Error("among equally playable candidates the higher resolution must win")
	}
}

// TestSelectionRespectsAResolutionCap — a phone gains nothing from pixels it
// cannot display and pays for them in bandwidth.
func TestSelectionRespectsAResolutionCap(t *testing.T) {
	phone := browser
	phone.MaxHeight = 1080

	if playbackScore(fourKNoSound, phone) >= playbackScore(sevenTwentyPlayable, phone) {
		t.Error("a candidate above the cap must be penalised below one within it")
	}
}

// TestSelectionFallsBackToTheSourceOrder guards the no-preference case. With
// nothing to rank against, the addon's own ordering is better than any ordering
// invented here — it knows its ecosystem and this does not.
func TestSelectionFallsBackToTheSourceOrder(t *testing.T) {
	none := PlaybackPreference{}
	if !none.Empty() {
		t.Fatal("an unset preference must report Empty")
	}
	if playbackScore(fourKNoSound, none) <= playbackScore(tenEightyPlayable, none) {
		t.Error("with no preference, the source's first-ranked candidate must win")
	}
}

// TestSelectionIgnoresUnknownMetadata is the honest case: the module's parse is
// best-effort and often finds nothing. A candidate with no parsed codecs must
// not be scored as though it were incompatible — it is merely unknown, and the
// probe settles it later (ADR 0050).
func TestSelectionIgnoresUnknownMetadata(t *testing.T) {
	unknown := v1.Part{ID: "p-?", NaturalOrder: 1}
	// mpeg4 and eac3 are both outside the browser set, so this one is known-bad
	// rather than merely unparsed.
	incompatible := v1.Part{ID: "p-x", NaturalOrder: 1, VideoCodec: "mpeg4", AudioCodec: "eac3"}

	if playbackScore(unknown, browser) <= playbackScore(incompatible, browser) {
		t.Error("an unparsed candidate must outrank one known to be undecodable")
	}

	// ...and must still lose to one known to be good, or a lucky guess would
	// beat a verified match.
	if playbackScore(unknown, browser) >= playbackScore(tenEightyPlayable, browser) {
		t.Error("an unparsed candidate must not outrank a known-playable one")
	}
}

// TestSelectionAvoidsHDRAClientCannotRender is the purple-and-green case moved
// one step earlier (ADR 0050). A browser decodes HEVC perfectly well and still
// renders an HDR10 stream as nonsense, so the fix is a tone-map, which is a full
// video re-encode — by far the most expensive outcome selection can cause.
// Preferring an SDR release of the same quality avoids it entirely.
func TestSelectionAvoidsHDRAClientCannotRender(t *testing.T) {
	hdr := v1.Part{
		ID: "p-hdr", NaturalOrder: 0, VideoCodec: "hevc", AudioCodec: "aac",
		Height: 1080, HDRFormat: "hdr10",
	}
	sdr := v1.Part{
		ID: "p-sdr", NaturalOrder: 4, VideoCodec: "hevc", AudioCodec: "aac",
		Height: 1080,
	}
	if playbackScore(sdr, browser) <= playbackScore(hdr, browser) {
		t.Errorf("an SDR release (%d) must outrank an HDR one (%d) for a client that cannot render it",
			playbackScore(sdr, browser), playbackScore(hdr, browser))
	}

	// A client that *can* render HDR must see no penalty at all, or televisions
	// would be steered away from the releases they exist to play.
	tv := PlaybackPreference{
		VideoCodecs: map[string]bool{"hevc": true}, AudioCodecs: map[string]bool{"aac": true}, HDR: true,
	}
	if playbackScore(hdr, tv) <= playbackScore(sdr, tv) {
		t.Error("an HDR-capable client must not be penalised for HDR content")
	}
}

// TestSelectionPenalisesHDRLessThanUndecodability keeps the two costs in the
// right order. Tone-mapped HDR does eventually play; a codec the client cannot
// decode never does.
func TestSelectionPenalisesHDRLessThanUndecodability(t *testing.T) {
	hdrPlayable := v1.Part{
		ID: "p-hdr", NaturalOrder: 0, VideoCodec: "hevc", AudioCodec: "aac",
		Height: 1080, HDRFormat: "hdr10",
	}
	sdrSilent := v1.Part{
		ID: "p-silent", NaturalOrder: 0, VideoCodec: "hevc", AudioCodec: "eac3",
		Height: 1080,
	}
	if playbackScore(hdrPlayable, browser) <= playbackScore(sdrSilent, browser) {
		t.Error("HDR needing a tone-map must outrank audio that cannot be decoded at all")
	}
}
