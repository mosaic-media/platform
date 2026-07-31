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

	// form is how this track can be delivered. Unexported and not serialised:
	// it decides which list a track lands in here, and by the time a ticket is
	// sealed that decision has already been made.
	form SubtitleForm
}

// SubtitleForm is how a subtitle track can be delivered, which is decided by its
// codec and not by anything a viewer chose (ADR 0114).
type SubtitleForm int

const (
	// SubtitlePlain is a text track carrying words and little else — SubRip and
	// its relatives. WebVTT represents everything it has, so a rendition is
	// faithful and costs nothing.
	SubtitlePlain SubtitleForm = iota
	// SubtitleTypeset is a text track that also carries where the words go and
	// what they look like — ASS and SSA, which is what anime releases use for
	// signs, songs and captions placed over the picture.
	//
	// A rendition keeps the words, the bold and the italics, and **loses the
	// position, the colour, the size and the alignment**. Measured: a cue
	// authored `{\pos(640,120)\c&H00FF00&\fs72}` over a doorway arrived as
	// ordinary bold text at the bottom of the screen.
	SubtitleTypeset
	// SubtitleGraphic is a track of pictures rather than words — PGS from a
	// Blu-ray, VobSub from a DVD, DVB from a broadcast. There is no text in it
	// to convert; ffmpeg refuses outright, with "subtitle encoding currently
	// only possible from text to text or bitmap to bitmap". Burning it into the
	// picture is the only delivery there is.
	SubtitleGraphic
)

// formOf classifies a track by the codec ffprobe reported.
//
// Unknown codecs are treated as plain text, which is the safe default in the
// direction that matters: a rendition that turns out empty costs one viewer
// their subtitles for one playback, where treating an unknown text codec as
// graphic would force a video encode on a release that never needed one.
func formOf(codec string) SubtitleForm {
	switch strings.ToLower(strings.TrimSpace(codec)) {
	case "ass", "ssa":
		return SubtitleTypeset
	case "hdmv_pgs_subtitle", "pgssub", "dvd_subtitle", "dvdsub", "dvb_subtitle", "dvbsub", "xsub":
		return SubtitleGraphic
	default:
		return SubtitlePlain
	}
}

// BurnedSubtitle names a track to render into the picture itself (ADR 0114).
//
// It is the last resort and it is priced like one: burning forces a video
// encode, because there is no way to draw on frames that are being copied
// through untouched. **It also fixes the choice for the whole playback** — a
// burned track cannot be switched off in the player's menu, since by then it is
// part of the picture.
type BurnedSubtitle struct {
	// Index is the stream's index in the source, used by the overlay path.
	Index int `json:"i"`
	// Ordinal is the track's position among the source's *subtitle* streams,
	// which is the numbering the `subtitles` filter's `si` option counts in and
	// is not the same as Index.
	Ordinal int `json:"o"`
	// Graphic selects the delivery. A picture track is composited with
	// `overlay`, reading the stream this run already has open; a text track is
	// rendered by libass, which can only read a file and therefore opens the
	// source a second time.
	Graphic bool `json:"g,omitempty"`
	// Label is what the track is, for telling a viewer what they are watching.
	Label string `json:"n,omitempty"`
}

// DecideSubtitles turns a release's subtitle tracks and one viewer's intent into
// what a playback offers, and what if anything it burns (ADR 0113, ADR 0114).
//
// The rule is ADR 0112's, applied to a list that already exists:
//
//   - **Every deliverable track is offered**, in the order the container carries
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
//
// **Burning is decided last and only when there is no other way**, which is the
// whole shape of ADR 0114. The chosen track is burned when it is graphic — there
// being no text in it to send — or when it is typeset and this viewer asked to
// see it as authored. Everything else is a rendition, because a rendition costs
// nothing and a burn costs a video encode.
//
// When a track is burned nothing is offered beside it. The burned track is
// already in the picture, and listing it again would draw it twice.
func DecideSubtitles(tracks []SubtitleTrack, intent SubtitleIntent, wantsTypeset bool) ([]SubtitleDelivery, *BurnedSubtitle) {
	if len(tracks) == 0 {
		return nil, nil
	}

	all := make([]SubtitleDelivery, 0, len(tracks))
	for _, t := range tracks {
		all = append(all, SubtitleDelivery{
			Index:    t.Index,
			Language: strings.ToLower(t.Language),
			Forced:   t.Forced,
			Label:    subtitleLabel(t),
			form:     formOf(t.Codec),
		})
	}

	// Nobody asked for anything, so nothing is chosen — but the deliverable
	// tracks are still listed, because turning subtitles on for one scene is not
	// a change of preference.
	if intent.Mode == SubtitlesOff {
		return renditions(all), nil
	}

	// The choice is made over *every* track, graphic ones included. A release
	// whose only English subtitles are a Blu-ray's pictures must still be able
	// to answer someone who asked for English.
	n := defaultSubtitle(all, intent)
	if n < 0 {
		return renditions(all), nil
	}

	chosen := all[n]
	if chosen.form == SubtitleGraphic || (chosen.form == SubtitleTypeset && wantsTypeset) {
		return nil, &BurnedSubtitle{
			Index:   chosen.Index,
			Ordinal: subtitleOrdinal(tracks, chosen.Index),
			Graphic: chosen.form == SubtitleGraphic,
			Label:   chosen.Label,
		}
	}

	out := renditions(all)
	for i := range out {
		if out[i].Index == chosen.Index {
			out[i].Default = true
		}
	}
	return out, nil
}

// renditions drops the tracks that cannot be one.
//
// A graphic track offered as a WebVTT rendition is the bug this function exists
// to prevent, and it shipped: the extraction fails, the origin answers 200 with
// an empty document, and the player lists a subtitle track that draws nothing
// for the length of the film.
func renditions(all []SubtitleDelivery) []SubtitleDelivery {
	var out []SubtitleDelivery
	for _, s := range all {
		if s.form != SubtitleGraphic {
			out = append(out, s)
		}
	}
	return out
}

// subtitleOrdinal reports a stream's position among the source's subtitle
// streams, which is what the `subtitles` filter's `si` option counts in.
func subtitleOrdinal(tracks []SubtitleTrack, index int) int {
	for i, t := range tracks {
		if t.Index == index {
			return i
		}
	}
	return 0
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

// escapeFilterValue renders a value safe to put inside an ffmpeg filtergraph.
//
// **There are two levels of escaping and both apply**, which is why the result
// looks wrong. A filtergraph is parsed once to split filters and their options,
// and each option value is unescaped again — so a colon in a URL needs a
// backslash to survive the option level, and that backslash needs a backslash to
// survive the graph level. `https://host/x` becomes `https\\://host/x`.
//
// Measured, because it fails silently rather than loudly: a single-escaped URL
// makes ffmpeg read `//host` as the value of an unrelated option and report
// "unable to parse option value as image size", after which it renders the video
// with no subtitles on it and exits successfully. Double-escaped, the same URL
// with a query string burned its cues at exactly the right timestamps.
func escapeFilterValue(s string) string {
	// Option level: a backslash, then the characters that separate options.
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, ":", `\:`)
	// Graph level: everything that separates filters, plus the backslashes just
	// added.
	s = strings.ReplaceAll(s, `\`, `\\`)
	for _, c := range []string{"'", "[", "]", ",", ";"} {
		s = strings.ReplaceAll(s, c, `\`+c)
	}
	return s
}

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
