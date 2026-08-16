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
// (platform#83's second half, platform#83).
//
// Offering is not selecting, and the whole file turns on that distinction. A
// viewer's preference decides what comes on by itself; it does not decide what
// they are allowed to reach. So every embedded track becomes a rendition the
// player lists, and exactly one — or none — is marked default. There is no track
// picker to build: the player already has a subtitle menu, and a rendition is
// how it gets populated.

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
// codec and not by anything a viewer chose (platform#83).
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
	// A rendition keeps the words, the bold and the italics, and loses the
	// position, the colour, the size and the alignment: a cue authored
	// {\pos(640,120)\c&H00FF00&\fs72} over a doorway arrives as ordinary bold
	// text at the bottom of the screen.
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

// BurnedSubtitle names a track to render into the picture itself (platform#83).
//
// It is the last resort and it is priced like one: burning forces a video
// encode, because there is no way to draw on frames that are being copied
// through untouched. It also fixes the choice for the whole playback — a burned
// track cannot be switched off in the player's menu, since by then it is part of
// the picture.
type BurnedSubtitle struct {
	// Index is the stream's index in the source, used by the overlay path.
	Index int `json:"i"`
	// Ordinal is the track's position among the source's subtitle streams, which
	// is the numbering the subtitles filter's si option counts in, and is not the
	// same as Index.
	Ordinal int `json:"o"`
	// Graphic selects the delivery. A picture track is composited with overlay,
	// reading the stream this run already has open; a text track is rendered by
	// libass, which can only read a file and therefore opens the source a second
	// time.
	Graphic bool `json:"g,omitempty"`
	// Label is what the track is, for telling a viewer what they are watching.
	Label string `json:"n,omitempty"`
}

// DecideSubtitles turns a release's subtitle tracks and one viewer's intent into
// what a playback offers, and what if anything it burns (platform#83, platform#83).
//
// The rule is platform#83's, applied to a list that already exists:
//
//   - Every deliverable track is offered, in the order the container carries
//     them. A menu that hid tracks would make the preference an access control
//     over the release rather than a default over it.
//   - At most one is default, and only in a language the viewer asked for. A
//     subtitle track in a language somebody cannot read occupies the screen and
//     communicates nothing, so it is listed and left off.
//   - off defaults nothing, having already survived the escalation.
//
// Within a preferred language the mode picks between what is there: forced wants
// a track the release marks forced and takes a full one only if there is no
// forced track to be had, full wants the other way round. Neither invents a
// track — a release with only forced subtitles and a viewer who asked for full
// gets the forced one, which is more than nothing and is what the release has.
//
// Burning is decided last and only when there is no other way. The chosen track
// is burned when it is graphic — there being no text in it to send — or when it
// is typeset and this viewer asked to see it as authored. Everything else is a
// rendition, because a rendition costs nothing and a burn costs a video
// encode.
//
// When a track is burned nothing is offered beside it. The burned track is
// already in the picture, and listing it again would draw it twice.
func DecideSubtitles(tracks []SubtitleTrack, intent SubtitleIntent, styling SubtitleStyling) ([]SubtitleDelivery, *BurnedSubtitle, []StyledSubtitle) {
	if len(tracks) == 0 {
		return nil, nil, nil
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
		return renditions(all), nil, nil
	}

	// The choice is made over every track, graphic ones included. A release
	// whose only English subtitles are a Blu-ray's pictures must still be able to
	// answer someone who asked for English.
	n := defaultSubtitle(all, intent)
	if n < 0 {
		return renditions(all), nil, nil
	}

	chosen := all[n]
	// A picture track has no other delivery. A styled one is burned only when
	// this viewer asked for it to be, because burning is the expensive answer
	// and sending the script to the client is the cheap one (platform#83).
	if chosen.form == SubtitleGraphic || (chosen.form == SubtitleTypeset && styling == StylingBurn) {
		return nil, &BurnedSubtitle{
			Index:   chosen.Index,
			Ordinal: subtitleOrdinal(tracks, chosen.Index),
			Graphic: chosen.form == SubtitleGraphic,
			Label:   chosen.Label,
		}, nil
	}

	out := renditions(all)
	for i := range out {
		if out[i].Index == chosen.Index {
			out[i].Default = true
		}
	}
	return out, nil, styled(all, tracks, chosen, styling)
}

// StyledSubtitle is a track offered to the client as it was authored, for a
// client that can draw it (platform#83).
//
// It rides beside the flattened rendition rather than replacing it, so a client
// that cannot render the script ignores this and uses the rendition, and one
// that can renders the signs where the author put them.
type StyledSubtitle struct {
	// Index is the stream's index in the source.
	Index int `json:"i"`
	// Ordinal is its position among the source's subtitle streams, which is what
	// the extraction's -map 0:s:N counts in.
	Ordinal int `json:"o"`
	// Language is the track's language code.
	Language string `json:"l,omitempty"`
	// Label is what a client's menu shows.
	Label string `json:"n,omitempty"`
	// Default marks the track the preference chose, so a client that draws these
	// knows which one to turn on without re-deriving the decision.
	Default bool `json:"d,omitempty"`
}

// styled lists the tracks worth sending as authored.
//
// Only typeset ones, and only when the viewer did not ask for them flattened.
// A plain track has nothing a script could carry that WebVTT does not, so
// offering one here would be a second copy of the same subtitles and a second
// read of the container to produce it.
func styled(all []SubtitleDelivery, tracks []SubtitleTrack, chosen SubtitleDelivery, styling SubtitleStyling) []StyledSubtitle {
	if styling != StylingClient {
		return nil
	}
	var out []StyledSubtitle
	for _, s := range all {
		if s.form != SubtitleTypeset {
			continue
		}
		out = append(out, StyledSubtitle{
			Index:    s.Index,
			Ordinal:  subtitleOrdinal(tracks, s.Index),
			Language: s.Language,
			Label:    s.Label,
			Default:  s.Index == chosen.Index,
		})
	}
	return out
}

// renditions drops the tracks that cannot be one.
//
// A graphic track offered as a WebVTT rendition is what this exists to prevent:
// the extraction fails, the origin answers 200 with an empty document, and the
// player lists a subtitle track that draws nothing for the length of the film.
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
// streams, which is what the subtitles filter's si option counts in.
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
// Ten times the video segment, and the ratio is the point. A subtitle segment
// costs one ffmpeg run and one range read over the container, because subtitle
// packets are interleaved with the video and there is no way to reach the text
// without reading past the pictures. Cutting subtitles on the video's six-second
// grid would pay that cost ten times as often for the same bytes, and HLS does
// not require the renditions to share a grid — only to describe the same running
// time.
//
// A minute is also comfortably more than any player reads ahead, so the window
// is fetched long before the video reaches it.
const subtitleWindow = 60 * time.Second

// StyledSubtitleName is the authored script for offered styled track i.
//
// Exported because the transport that mints a ticket has to point the client at
// it, like PlaylistName and for the same reason: a second spelling of the same
// path is exactly the drift this avoids.
//
// The .ass extension rather than a generic one because it is what the file is,
// and a client choosing a renderer reads the extension before it reads a byte.
func StyledSubtitleName(i int) string { return "styled" + strconv.Itoa(i) + ".ass" }

// styledSubtitleOf reads a styled-track index out of a resource name, accepting
// only the exact form the player node emits.
func styledSubtitleOf(resource string) (int, bool) {
	name, cut := strings.CutSuffix(resource, ".ass")
	if !cut || !strings.HasPrefix(name, "styled") {
		return 0, false
	}
	name = strings.TrimPrefix(name, "styled")
	i, err := strconv.Atoi(name)
	if err != nil || i < 0 || strconv.Itoa(i) != name {
		return 0, false
	}
	return i, true
}

// assMimeType is what an authored subtitle script is served as. There is no
// registered type for it, and this is the spelling libass-based clients look
// for.
const assMimeType = "text/x-ssa"

// escapeFilterValue renders a value safe to put inside an ffmpeg filtergraph.
//
// There are two levels of escaping and both apply, which is why the result looks
// wrong. A filtergraph is parsed once to split filters and their options, and
// each option value is unescaped again — so a colon in a URL needs a backslash to
// survive the option level, and that backslash needs a backslash to survive the
// graph level: https://host/x becomes https\\://host/x.
//
// It fails silently rather than loudly. A single-escaped URL makes ffmpeg read
// //host as the value of an unrelated option and report "unable to parse option
// value as image size", after which it renders the video with no subtitles on it
// and exits successfully.
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

// WithSubtitleOverride turns on the track a viewer chose for this sitting,
// instead of the one their preference chose (platform#71).
//
// It moves the default and changes nothing else: the same tracks are offered,
// in the same order, because an override is a choice among what is there rather
// than a new opinion about what should be. An index naming no offered track
// leaves the preference's choice alone — a stale menu is a worse reason to lose
// subtitles than no menu at all.
func WithSubtitleOverride(offered []SubtitleDelivery, styled []StyledSubtitle, index *int) {
	if index == nil {
		return
	}
	found := false
	for i := range offered {
		if offered[i].Index == *index {
			found = true
		}
	}
	for i := range styled {
		if styled[i].Index == *index {
			found = true
		}
	}
	if !found {
		return
	}
	for i := range offered {
		offered[i].Default = offered[i].Index == *index
	}
	for i := range styled {
		styled[i].Default = styled[i].Index == *index
	}
}

// ExternalSubtitle is a subtitle file a module found somewhere else (platform#83).
//
// It differs from every other track here in one way that decides the design: it
// is already a complete file, and it is not in the release. So there is nothing
// to extract and no container to read past — the origin fetches it, converts it
// if it is not already WebVTT, and serves it. That is far cheaper than the
// embedded path, which is why a remote source with no subtitles of its own is
// better served this way than by extraction.
type ExternalSubtitle struct {
	// URL is where the module said the file is. It is sealed in the ticket and
	// never sent to a client: a module resolves and the Platform serves
	// (platform#25), and the URL may carry a credential.
	URL string `json:"u"`
	// Language is the source's own label, and Label what a menu shows.
	Language string `json:"l,omitempty"`
	Label    string `json:"n,omitempty"`
	// Module is who offered it, so two sources' answers are tellable apart in a
	// menu that lists both.
	Module string `json:"m,omitempty"`
}

// externalSubtitleName is the origin's path for external track i.
func externalSubtitleName(i int) string { return "ext" + strconv.Itoa(i) + ".vtt" }

// ExternalSubtitleName is externalSubtitleName for the transport that mints the
// ticket, on the same terms as StyledSubtitleName.
func ExternalSubtitleName(i int) string { return externalSubtitleName(i) }

// externalSubtitleOf reads an external-track index out of a resource name,
// accepting only the exact form the player node emits.
func externalSubtitleOf(resource string) (int, bool) {
	name, cut := strings.CutSuffix(resource, ".vtt")
	if !cut || !strings.HasPrefix(name, "ext") {
		return 0, false
	}
	name = strings.TrimPrefix(name, "ext")
	i, err := strconv.Atoi(name)
	if err != nil || i < 0 || strconv.Itoa(i) != name {
		return 0, false
	}
	return i, true
}
