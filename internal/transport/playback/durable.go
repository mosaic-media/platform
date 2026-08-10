// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"encoding/json"
	"time"
)

// The durable form of a probe (platform#29).
//
// A probe describes the bytes. The bytes do not change, so probing the same
// release twice is pure waste — and it was the expensive kind: with the
// resolution cache in place, ffprobe against the remote URL became the largest
// single cost between a click and a first frame.
//
// platform#29 says probe results live on the Part, and points at the technical
// columns that were sitting empty waiting for them. **Those columns are not
// enough**, and the record's own worked example is why: a release with four
// audio tracks whose first is Hindi. `Part.AudioCodec` is one string; it cannot
// say which track, in which language, with how many channels. Persisting only
// the summary would let the second play skip the probe and then choose a
// different audio track from the first — a regression the columns could not even
// express. So the whole track list is persisted as a document, and the columns
// carry the summary they were always meant to carry.

// probeDocVersion is the schema version of the stored document.
//
// It exists so a future change to what a probe records is a re-probe rather than
// a misread. An unrecognised version decodes as "not probed", which costs one
// ffprobe run and cannot produce a wrong plan — the opposite trade from
// attempting a best-effort read of a shape this build does not know.
// Bumped to 2 when Duration was added. A version 1 document decodes as "not
// probed" and costs one ffprobe run; read back as version 2 it would decode
// with a zero duration, which is indistinguishable from a source that has none
// and would leave every already-probed release permanently unseekable. That is
// exactly the misread this version exists to prevent.
const probeDocVersion = 2

// probeDoc is MediaInfo as it is stored. It is a separate type from MediaInfo on
// purpose: MediaInfo is free to change shape with the code that uses it, and
// this one is a persisted format that may only change with its version.
type probeDoc struct {
	Version   int    `json:"v"`
	Container string `json:"container,omitempty"`
	SizeBytes int64  `json:"sizeBytes,omitempty"`
	// Nanoseconds. Stored because the origin needs it on every range request a
	// seeking player makes, and re-probing per request is not affordable.
	DurationNS int64          `json:"durationNs,omitempty"`
	Video      videoDoc       `json:"video"`
	Audio      []audioDoc     `json:"audio,omitempty"`
	Subtitles  []subtitlesDoc `json:"subtitles,omitempty"`
}

type videoDoc struct {
	Index     int    `json:"index"`
	Codec     string `json:"codec,omitempty"`
	Width     int    `json:"width,omitempty"`
	Height    int    `json:"height,omitempty"`
	Profile   string `json:"profile,omitempty"`
	PixelFmt  string `json:"pixelFmt,omitempty"`
	HDRFormat string `json:"hdrFormat,omitempty"`
}

type audioDoc struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec,omitempty"`
	Channels int    `json:"channels,omitempty"`
	Language string `json:"language,omitempty"`
	Title    string `json:"title,omitempty"`
	Default  bool   `json:"default,omitempty"`
}

type subtitlesDoc struct {
	Index    int    `json:"index"`
	Codec    string `json:"codec,omitempty"`
	Language string `json:"language,omitempty"`
	Forced   bool   `json:"forced,omitempty"`
}

// Encode renders a probe result for storage on the Part.
func Encode(info MediaInfo) ([]byte, error) {
	doc := probeDoc{
		Version:    probeDocVersion,
		Container:  info.Container,
		SizeBytes:  info.SizeBytes,
		DurationNS: int64(info.Duration),
		Video: videoDoc{
			Index: info.Video.Index, Codec: info.Video.Codec,
			Width: info.Video.Width, Height: info.Video.Height,
			Profile: info.Video.Profile, PixelFmt: info.Video.PixelFmt,
			HDRFormat: info.Video.HDRFormat,
		},
	}
	for _, a := range info.Audio {
		doc.Audio = append(doc.Audio, audioDoc{
			Index: a.Index, Codec: a.Codec, Channels: a.Channels,
			Language: a.Language, Title: a.Title, Default: a.Default,
		})
	}
	for _, s := range info.Subtitles {
		doc.Subtitles = append(doc.Subtitles, subtitlesDoc{
			Index: s.Index, Codec: s.Codec, Language: s.Language, Forced: s.Forced,
		})
	}
	return json.Marshal(doc)
}

// Decode reads a stored probe back. It reports false for anything it cannot use
// — absent, malformed, or written by a version this build does not know — and
// the caller re-probes, which is always safe because the bytes have not moved.
func Decode(raw []byte) (MediaInfo, bool) {
	if len(raw) == 0 {
		return MediaInfo{}, false
	}
	var doc probeDoc
	if err := json.Unmarshal(raw, &doc); err != nil {
		return MediaInfo{}, false
	}
	if doc.Version != probeDocVersion {
		return MediaInfo{}, false
	}

	info := MediaInfo{
		Container: doc.Container,
		SizeBytes: doc.SizeBytes,
		Duration:  time.Duration(doc.DurationNS),
		Video: VideoTrack{
			Index: doc.Video.Index, Codec: doc.Video.Codec,
			Width: doc.Video.Width, Height: doc.Video.Height,
			Profile: doc.Video.Profile, PixelFmt: doc.Video.PixelFmt,
			HDRFormat: doc.Video.HDRFormat,
		},
	}
	for _, a := range doc.Audio {
		info.Audio = append(info.Audio, AudioTrack{
			Index: a.Index, Codec: a.Codec, Channels: a.Channels,
			Language: a.Language, Title: a.Title, Default: a.Default,
		})
	}
	for _, s := range doc.Subtitles {
		info.Subtitles = append(info.Subtitles, SubtitleTrack{
			Index: s.Index, Codec: s.Codec, Language: s.Language, Forced: s.Forced,
		})
	}
	return info, true
}

// SummaryAudioCodec names the audio codec to record on the Part alongside the
// document.
//
// It is the codec of the track the *plan* would choose, not the first track in
// the file, and the difference matters because that column feeds candidate
// ranking (platform#27). Ranking asks "will this release need an audio encode for
// this client", and the only honest answer is about the track that would
// actually be played — on the Hindi-first release, the first track's codec would
// answer a question nobody asked.
func SummaryAudioCodec(info MediaInfo) string {
	// PreferredLanguages explicitly, and it stays install-wide on purpose now
	// that language is a person's preference (platform#67). This column feeds
	// *candidate ranking*, which asks "will this release need an audio encode at
	// all" — a coarse question one answer serves. Keying it per viewer is not
	// possible anyway: it is one column on a Part that four people share, so it
	// would be wrong for three of them silently. Per-user selection reads the
	// full track list from the stored probe document instead, which is where the
	// question "which track does *this* viewer get" is actually answered.
	//
	// chooseAudio does not apply the default the way Decide does, and an empty
	// preference list ranks an untagged track above a tagged one — which would
	// summarise the wrong track on exactly the multi-language release this
	// exists for.
	track, ok := chooseAudio(info.Audio, PreferredLanguages)
	if !ok {
		return ""
	}
	return track.Codec
}
