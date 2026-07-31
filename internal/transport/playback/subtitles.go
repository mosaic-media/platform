// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"strconv"
	"strings"
	"time"
)

// Which embedded subtitle tracks a playback offers, and which one is on
// (ADR 0112's second half, ADR 0113).
//
// **Offering is not selecting**, and the whole file turns on that distinction.
// A viewer's preference decides what comes on by itself; it does not decide what
// they are allowed to reach. So every embedded track becomes a rendition the
// player lists, and exactly one — or none — is marked default. That is also the
// only part of a track picker (M3 item 6) this slice delivers, and it delivers
// it by not building one: the player already has a subtitle menu, and a
// rendition is how it gets populated.

// SubtitleDelivery is one subtitle track offered to the client.
type SubtitleDelivery struct {
	// Index is the stream's index in the source, which is what the extraction
	// maps. It is the source's numbering rather than a position in this list,
	// for the same reason the audio plan names a stream index: a release whose
	// tracks are not in the order anyone expects must still play the right one.
	Index int `json:"i"`
	// Language is the track's language code as the container reports it, empty
	// when it carries none.
	Language string `json:"l,omitempty"`
	// Forced marks a track that translates on-screen text rather than
	// transcribing dialogue.
	Forced bool `json:"f,omitempty"`
	// Default marks the one track that comes on by itself. At most one is set,
	// and none is set when the viewer asked for no subtitles.
	Default bool `json:"d,omitempty"`
	// Label is what the player's menu shows.
	Label string `json:"n,omitempty"`
}

// DecideSubtitles turns a release's subtitle tracks and one viewer's intent into
// the renditions a playback offers (ADR 0113).
//
// The rule is ADR 0112's, applied to a list that already exists:
//
//   - **Every embedded track is offered**, in the order the container carries
//     them. A menu that hid tracks would make the preference an access control
//     over the release rather than a default over it.
//   - **At most one is default, and only in a language the viewer asked for.**
//     "A language nobody asked for is never selected" is the ADR's wording and it
//     is about selection: a subtitle track in a language somebody cannot read
//     occupies the screen and communicates nothing, so it is listed and left off.
//   - **`off` defaults nothing**, having already survived the escalation.
//
// Within a preferred language the mode picks between what is there: `forced`
// wants a track the release marks forced and takes a full one only if there is
// no forced track to be had, `full` wants the other way round. Neither invents a
// track — a release with only forced subtitles and a viewer who asked for full
// gets the forced one, which is more than nothing and is what the release has.
func DecideSubtitles(tracks []SubtitleTrack, intent SubtitleIntent) []SubtitleDelivery {
	if len(tracks) == 0 {
		return nil
	}

	out := make([]SubtitleDelivery, 0, len(tracks))
	for _, t := range tracks {
		out = append(out, SubtitleDelivery{
			Index:    t.Index,
			Language: strings.ToLower(t.Language),
			Forced:   t.Forced,
			Label:    subtitleLabel(t),
		})
	}
	if intent.Mode == SubtitlesOff {
		return out
	}
	if n := defaultSubtitle(out, intent); n >= 0 {
		out[n].Default = true
	}
	return out
}

// defaultSubtitle reports which offered track should come on by itself, or -1
// for none.
//
// It walks the viewer's languages in their order rather than the release's,
// because the preference is a ranking and the container's order is an accident
// of how it was built.
func defaultSubtitle(offered []SubtitleDelivery, intent SubtitleIntent) int {
	wantForced := intent.Mode == SubtitlesForced
	for _, lang := range intent.Languages {
		lang = strings.ToLower(strings.TrimSpace(lang))
		if lang == "" {
			continue
		}
		// Two passes over the same language: the mode's preference first, then
		// whatever else that language has. A viewer who asked for forced
		// subtitles on a release that ships none is better served by the full
		// track than by silence, and the reverse holds for the same reason.
		if n := findSubtitle(offered, lang, wantForced); n >= 0 {
			return n
		}
		if n := findSubtitle(offered, lang, !wantForced); n >= 0 {
			return n
		}
	}
	return -1
}

// findSubtitle reports the first offered track in language with the given forced
// disposition, or -1.
func findSubtitle(offered []SubtitleDelivery, language string, forced bool) int {
	for i, s := range offered {
		if s.Language == language && s.Forced == forced {
			return i
		}
	}
	return -1
}

// subtitleLabel names a track for a player's menu.
//
// The language code is what a container reliably carries, and it is not what
// anybody wants to read, so the known ones are spelled out. An unknown code is
// shown as itself rather than as "Unknown": a viewer choosing between "swe" and
// "dan" can act on that, and choosing between two entries both reading "Unknown"
// cannot.
func subtitleLabel(t SubtitleTrack) string {
	name := subtitleLanguageNames[strings.ToLower(t.Language)]
	if name == "" {
		name = strings.ToLower(t.Language)
	}
	if name == "" || name == "und" {
		name = "Subtitles"
	}
	if t.Forced {
		name += " (forced)"
	}
	return name
}

// subtitleLanguageNames spells out the codes the offered languages use, in both
// the three-letter and two-letter forms a container may carry.
var subtitleLanguageNames = map[string]string{
	"eng": "English", "en": "English",
	"jpn": "Japanese", "ja": "Japanese",
	"spa": "Spanish", "es": "Spanish",
	"fra": "French", "fre": "French", "fr": "French",
	"deu": "German", "ger": "German", "de": "German",
	"ita": "Italian", "it": "Italian",
	"por": "Portuguese", "pt": "Portuguese",
	"kor": "Korean", "ko": "Korean",
	"zho": "Chinese", "chi": "Chinese", "zh": "Chinese",
}

// subtitleWindow is how much of the release one subtitle segment covers.
//
// **Ten times the video segment, and the ratio is the point.** A subtitle
// segment costs one ffmpeg run and one range read over the container, because
// subtitle packets are interleaved with the video and there is no way to reach
// the text without reading past the pictures. Cutting subtitles on the video's
// six-second grid would pay that cost ten times as often for the same bytes,
// and HLS does not require the renditions to share a grid — only to describe the
// same running time.
//
// A minute is also comfortably more than any player reads ahead, so the window
// is fetched long before the video reaches it.
const subtitleWindow = 60 * time.Second

// subtitlePlaylistName is the rendition playlist for offered track i.
func subtitlePlaylistName(i int) string { return "s" + strconv.Itoa(i) + ".m3u8" }

// subtitleSegmentName is segment n of offered track i.
//
// Flat rather than nested, because the origin's routing allows exactly one path
// element under a ticket — a resource that arrives in a URL is matched against a
// closed set and never used as a path.
func subtitleSegmentName(i, n int) string {
	return "s" + strconv.Itoa(i) + "_" + strconv.Itoa(n) + ".vtt"
}

// subtitleSegmentOf reads an offered-track index and a segment index out of a
// resource name, accepting only the exact form the playlist emits.
func subtitleSegmentOf(resource string) (track, segment int, ok bool) {
	name, cut := strings.CutSuffix(resource, ".vtt")
	if !cut || !strings.HasPrefix(name, "s") {
		return 0, 0, false
	}
	left, right, found := strings.Cut(name[1:], "_")
	if !found {
		return 0, 0, false
	}
	track, err := strconv.Atoi(left)
	if err != nil || track < 0 || strconv.Itoa(track) != left {
		return 0, 0, false
	}
	segment, err = strconv.Atoi(right)
	if err != nil || segment < 0 || strconv.Itoa(segment) != right {
		return 0, 0, false
	}
	return track, segment, true
}

// subtitlePlaylistOf reads an offered-track index out of a rendition playlist
// name, on the same terms.
func subtitlePlaylistOf(resource string) (int, bool) {
	name, cut := strings.CutSuffix(resource, ".m3u8")
	if !cut || !strings.HasPrefix(name, "s") {
		return 0, false
	}
	name = name[1:]
	i, err := strconv.Atoi(name)
	if err != nil || i < 0 || strconv.Itoa(i) != name {
		return 0, false
	}
	return i, true
}
