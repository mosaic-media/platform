// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"fmt"
	"strings"

	"github.com/mosaic-media/contracts/ui"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/transport/playback"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The technical facts a detail screen states about the release behind its play
// button: what it is, how it will be delivered, and where its description came
// from.
//
// Every answer here already existed and was thrown away. The probe (platform#29) is
// stored on the Part and read only by the per-stream encode decision; the
// delivery plan is computed at play time, used to mint one ticket and dropped;
// the provider that answered is in the ref. The screen that would tell a viewer
// why something will or will not play well was the one surface never given any
// of it.

// releaseFacts decodes what is known about a release, from the Part's scalars
// and the probe document stored on it. A Part with no probe still yields the
// scalars, which is the ordinary state before something has been played once.
type releaseFacts struct {
	part  v1.Part
	probe playback.MediaInfo
	// probed distinguishes "this release has no audio tracks" from "nobody has
	// looked yet", which read identically in an empty track list and mean
	// opposite things on screen.
	probed bool
}

// factsFor reads a Part and its stored probe.
func factsFor(part v1.Part) releaseFacts {
	f := releaseFacts{part: part}
	if raw := app.PartProbe(part); len(raw) > 0 {
		if info, ok := playback.Decode(raw); ok {
			f.probe, f.probed = info, true
		}
	}
	return f
}

// qualityPill is a release's resolution and dynamic range the way a viewer reads
// it — "4K HDR", "1080p". Empty when the height is unknown, which is the
// unprobed case rather than a zero-height video.
//
// Deliberately coarser than the Video card below. A pill answers "will this look
// good"; "3840×2160 Dolby Vision Profile 8" answers "why", and putting the
// second where the first belongs makes a meta line unreadable.
func (f releaseFacts) qualityPill() string {
	if f.part.Height <= 0 {
		return ""
	}
	q := heightLabel(f.part.Height)
	if f.part.HDRFormat != "" {
		q += " HDR"
	}
	return q
}

// heightLabel names a vertical resolution the way a viewer does: the marketing
// name where one exists and the line count where it does not. "4K" rather than
// "2160p" because that is what is written on the box.
func heightLabel(h int) string {
	switch {
	case h >= 2160:
		return "4K"
	case h >= 1440:
		return "1440p"
	case h >= 1080:
		return "1080p"
	case h >= 720:
		return "720p"
	default:
		return fmt.Sprintf("%dp", h)
	}
}

// audioPill is the release's primary audio as one short phrase — "Atmos 5.1",
// "Stereo". Empty when nothing is known about the audio.
func (f releaseFacts) audioPill() string {
	t, ok := f.primaryAudio()
	if !ok {
		// Without a probe there is still a codec on the Part, which is worth
		// naming even though the channel layout is not known from it.
		return strings.ToUpper(f.part.AudioCodec)
	}
	parts := make([]string, 0, 2)
	// Atmos is not a codec — it is an object-based extension carried inside one,
	// and ffprobe reports it in the stream title rather than as a codec name. So
	// it is read from where it actually is rather than inferred from E-AC3,
	// which is present on plenty of releases that carry no Atmos at all.
	if strings.Contains(strings.ToLower(t.Title), "atmos") {
		parts = append(parts, "Atmos")
	}
	if ch := channelLabel(t.Channels); ch != "" {
		parts = append(parts, ch)
	}
	if len(parts) == 0 {
		return strings.ToUpper(t.Codec)
	}
	return strings.Join(parts, " ")
}

// primaryAudio is the track a viewer will actually hear — the default one, or
// the first when none is marked.
func (f releaseFacts) primaryAudio() (playback.AudioTrack, bool) {
	if !f.probed || len(f.probe.Audio) == 0 {
		return playback.AudioTrack{}, false
	}
	for _, t := range f.probe.Audio {
		if t.Default {
			return t, true
		}
	}
	return f.probe.Audio[0], true
}

// channelLabel names a channel count the way a viewer reads it.
func channelLabel(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	default:
		return fmt.Sprintf("%d.0", n)
	}
}

// subtitlesLabel names what a viewer can read along with, "English · 2 more".
// Empty when the release carries none, which is a real answer and different from
// an unprobed one — an unprobed release also returns empty, and the caller drops
// the row either way rather than claiming there are no subtitles.
func (f releaseFacts) subtitlesLabel() string {
	if !f.probed || len(f.probe.Subtitles) == 0 {
		return ""
	}
	first := languageName(f.probe.Subtitles[0].Language)
	if first == "" {
		first = "Subtitles"
	}
	if n := len(f.probe.Subtitles) - 1; n > 0 {
		return fmt.Sprintf("%s · %d more", first, n)
	}
	return first
}

// videoCard states what the video stream is.
//
// The mockups put a frame rate on its third line. There is none here: the probe
// document carries no frame rate — `playback.VideoTrack` has no such field and
// the ffprobe parse never reads `r_frame_rate` — so the line is omitted rather
// than filled with a plausible 24.000.
func (f releaseFacts) videoCard() ui.El {
	lines := make([]string, 0, 3)
	if c := f.videoCodecName(); c != "" {
		lines = append(lines, c)
	}
	if f.part.Width > 0 && f.part.Height > 0 {
		lines = append(lines, fmt.Sprintf("%d×%d", f.part.Width, f.part.Height))
	}
	if hdr := f.hdrName(); hdr != "" {
		lines = append(lines, hdr)
	}
	if len(lines) == 0 {
		return nil
	}
	return ui.FactCard("Video", ui.Lines(lines...))
}

// videoCodecName is the codec as a viewer would recognise it, preferring the
// probe's answer over whatever the module parsed out of a release name — which
// is platform#29's whole point.
func (f releaseFacts) videoCodecName() string {
	c := f.part.VideoCodec
	if f.probed && f.probe.Video.Codec != "" {
		c = f.probe.Video.Codec
	}
	return strings.ToUpper(c)
}

// hdrName is the dynamic range with the profile the probe found, when there is
// one — "Dolby Vision Profile 8", "HDR10".
func (f releaseFacts) hdrName() string {
	if f.part.HDRFormat == "" {
		return ""
	}
	name := f.part.HDRFormat
	if f.probed && f.probe.Video.Profile != "" {
		return name + " " + f.probe.Video.Profile
	}
	return name
}

// audioCard states what the audio streams are, and how many of them there are.
//
// The mockups put a per-track bitrate on its second line. There is none here:
// `playback.AudioTrack` carries no bitrate, so the line names the language
// instead and the number is omitted rather than invented.
func (f releaseFacts) audioCard() ui.El {
	lines := make([]string, 0, 3)
	t, ok := f.primaryAudio()
	switch {
	case ok:
		head := strings.ToUpper(t.Codec)
		if extra := f.audioPill(); extra != "" && !strings.EqualFold(extra, t.Codec) {
			head += " " + extra
		}
		lines = append(lines, head)
		if lang := languageName(t.Language); lang != "" {
			lines = append(lines, lang)
		}
		if n := len(f.probe.Audio) - 1; n > 0 {
			lines = append(lines, fmt.Sprintf("+%d more %s", n, plural(n, "track")))
		}
	case f.part.AudioCodec != "":
		lines = append(lines, strings.ToUpper(f.part.AudioCodec))
	}
	if len(lines) == 0 {
		return nil
	}
	return ui.FactCard("Audio", ui.Lines(lines...))
}

// deliveryCard states what will happen to the bytes on the way to this client
// (platform#29) — the answer that decides whether playback will be instant or will
// cost a re-encode, and the one a viewer can act on by choosing another release.
//
// It is stated against what *this* client declared it can decode, not against a
// house assumption: the same file direct-plays in Safari and is remuxed for
// Chrome, and a card that said otherwise would be wrong for one of them.
func (f releaseFacts) deliveryCard(codecs playback.ClientCodecs) ui.El {
	if !f.probed {
		// Without a probe there is no honest answer. The plan would be computed
		// from an empty track list and would say "direct play" about a file
		// nobody has looked inside.
		return nil
	}
	plan := playback.Decide(f.probe, codecs, nil)
	lines := make([]string, 0, 3)
	if plan.DirectPlay {
		lines = append(lines, "Direct play", "No transcode needed")
	} else {
		lines = append(lines, "Remuxed on the fly")
		if plan.Reason != "" {
			lines = append(lines, plan.Reason)
		}
	}
	if b := bitrateLabel(f.part.BitrateBPS); b != "" {
		lines = append(lines, b)
	}
	return ui.FactCard("Delivery", ui.Lines(lines...))
}

// bitrateLabel renders an average bitrate for a human, empty when unknown.
func bitrateLabel(bps int64) string {
	if bps <= 0 {
		return ""
	}
	if mbps := float64(bps) / 1_000_000; mbps >= 1 {
		return fmt.Sprintf("%.0f Mbps average", mbps)
	}
	return fmt.Sprintf("%d kbps average", bps/1000)
}

// metadataCard names where this description came from.
//
// The mockups put "Refreshed 4h ago" on its third line. There is none here:
// PreviewContent asks the provider on every render and neither
// `v1.ContentMetadata` nor its result carries a retrieval time, so there is no
// age to state.
//
// Artwork is attributed to the same provider rather than to an artwork module,
// because on this path it *is* the same provider — PreviewContent reads one
// metadata source and never consults RoleArtwork.
func metadataCard(ref v1.ContentRef, providerName string) ui.El {
	name := providerName
	if name == "" {
		name = ref.Provider
	}
	if name == "" {
		return nil
	}
	return ui.FactCard("Metadata", ui.Lines(
		name+" matched",
		"Artwork from "+name,
	))
}

// plural is the crudest possible pluraliser, for the two nouns here.
func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

// languageName maps an ISO 639 code to a name a viewer reads, falling back to
// the code as the source wrote it.
//
// A short table rather than a dependency: these are the languages a media
// library actually carries tracks in, and an unrecognised code appears as
// itself, which is what a subtitle menu has always done.
func languageName(code string) string {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "":
		return ""
	case "en", "eng":
		return "English"
	case "es", "spa":
		return "Spanish"
	case "fr", "fra", "fre":
		return "French"
	case "de", "deu", "ger":
		return "German"
	case "it", "ita":
		return "Italian"
	case "pt", "por":
		return "Portuguese"
	case "nl", "nld", "dut":
		return "Dutch"
	case "sv", "swe":
		return "Swedish"
	case "no", "nor":
		return "Norwegian"
	case "da", "dan":
		return "Danish"
	case "fi", "fin":
		return "Finnish"
	case "pl", "pol":
		return "Polish"
	case "ru", "rus":
		return "Russian"
	case "ja", "jpn":
		return "Japanese"
	case "ko", "kor":
		return "Korean"
	case "zh", "zho", "chi":
		return "Chinese"
	case "hi", "hin":
		return "Hindi"
	case "ar", "ara":
		return "Arabic"
	case "tr", "tur":
		return "Turkish"
	case "cs", "ces", "cze":
		return "Czech"
	case "hu", "hun":
		return "Hungarian"
	case "el", "ell", "gre":
		return "Greek"
	case "he", "heb":
		return "Hebrew"
	case "th", "tha":
		return "Thai"
	case "uk", "ukr":
		return "Ukrainian"
	default:
		return strings.ToUpper(code)
	}
}
