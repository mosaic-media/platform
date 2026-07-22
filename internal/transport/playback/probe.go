// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"context"
	"encoding/json"
	"errors"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// Probing (ADR 0050). What a release *is* cannot be read off its name: the same
// fact lives in a different field per addon, a release title lies, and the
// container hint has already been found hiding in a query parameter. ffprobe
// asks the bytes instead — and it range-requests the header rather than
// downloading the file, so asking is cheap even when the file is thirty
// gigabytes.
//
// It answers the question that actually decides playback, which the container
// never did: **which audio tracks are here, in what codec, in what language.**

// probeTimeout bounds a probe. ffprobe against a remote URL is a couple of
// range requests; if it has not answered in this long the source is unwell and
// falling back to an unprobed relay is better than making a user wait.
const probeTimeout = 45 * time.Second

// ErrProbeUnavailable reports that ffprobe is not installed. It is distinct
// because the consequence is specific: without it Mosaic cannot tell an
// undecodable audio track from a decodable one, so it can only relay and hope.
var ErrProbeUnavailable = errors.New("playback: ffprobe is not available")

// MediaInfo is what a probe learned about one release.
type MediaInfo struct {
	// Container is ffprobe's format name, normalised to the first alternative —
	// it reports "matroska,webm" for a Matroska file, and only the first is
	// meaningful for a decision.
	Container string
	SizeBytes int64
	Video     VideoTrack
	Audio     []AudioTrack
	Subtitles []SubtitleTrack
}

// VideoTrack is the release's video stream. There is one in every case that
// matters here; a release with several is not something to guess about.
type VideoTrack struct {
	Index     int
	Codec     string
	Width     int
	Height    int
	Profile   string
	PixelFmt  string
	HDRFormat string
}

// AudioTrack is one audio stream, with the two things a decision needs: whether
// the client can decode it, and whether it is the language anyone wants.
type AudioTrack struct {
	Index    int
	Codec    string
	Channels int
	Language string
	Title    string
	Default  bool
}

// SubtitleTrack is one subtitle stream. Nothing consumes these yet; they are
// carried because the probe returns them for free and a track list is what a
// player's subtitle menu will need.
type SubtitleTrack struct {
	Index    int
	Codec    string
	Language string
	Forced   bool
}

// Prober runs ffprobe.
type Prober struct {
	binary string
}

// NewProber looks for ffprobe on PATH. A Prober with no binary is valid and
// reports Available() false: probing is an enhancement over relaying blind, and
// the Platform must still start and direct-play without it.
func NewProber() *Prober {
	bin, err := exec.LookPath("ffprobe")
	if err != nil {
		return &Prober{}
	}
	return &Prober{binary: bin}
}

// NewProberAt builds a Prober over an explicit binary path, for a deployment
// that ships ffprobe somewhere other than PATH, and for tests.
func NewProberAt(binary string) *Prober { return &Prober{binary: binary} }

// Available reports whether a probe can actually be performed.
func (p *Prober) Available() bool { return p != nil && p.binary != "" }

// Probe asks ffprobe what is in the stream at url.
//
// Headers are passed through in the CRLF form ffprobe expects, so a credentialed
// upstream is probeable on the same terms it is playable — a probe that cannot
// authenticate would report nothing and silently degrade every decision built
// on it.
func (p *Prober) Probe(ctx context.Context, url string, headers map[string]string) (MediaInfo, error) {
	if !p.Available() {
		return MediaInfo{}, ErrProbeUnavailable
	}

	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()

	args := []string{"-v", "error", "-print_format", "json", "-show_format", "-show_streams"}
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	args = append(args, url)

	out, err := exec.CommandContext(ctx, p.binary, args...).Output()
	if err != nil {
		return MediaInfo{}, err
	}
	return parseProbe(out)
}

// ffprobeOutput mirrors the subset of ffprobe's JSON this reads.
type ffprobeOutput struct {
	Format struct {
		FormatName string `json:"format_name"`
		Size       string `json:"size"`
	} `json:"format"`
	Streams []struct {
		Index         int    `json:"index"`
		CodecName     string `json:"codec_name"`
		CodecType     string `json:"codec_type"`
		Profile       string `json:"profile"`
		Width         int    `json:"width"`
		Height        int    `json:"height"`
		Channels      int    `json:"channels"`
		PixFmt        string `json:"pix_fmt"`
		ColorTransfer string `json:"color_transfer"`
		Disposition   struct {
			Default int `json:"default"`
			Forced  int `json:"forced"`
		} `json:"disposition"`
		Tags struct {
			Language string `json:"language"`
			Title    string `json:"title"`
		} `json:"tags"`
	} `json:"streams"`
}

// parseProbe turns ffprobe's JSON into a MediaInfo.
func parseProbe(raw []byte) (MediaInfo, error) {
	var out ffprobeOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return MediaInfo{}, err
	}

	info := MediaInfo{Container: primaryFormat(out.Format.FormatName)}
	if n, err := strconv.ParseInt(out.Format.Size, 10, 64); err == nil {
		info.SizeBytes = n
	}

	for _, s := range out.Streams {
		switch s.CodecType {
		case "video":
			// First video stream wins. A release with several is usually a
			// cover image or a thumbnail track, not a second feature.
			if info.Video.Codec != "" {
				continue
			}
			info.Video = VideoTrack{
				Index: s.Index, Codec: s.CodecName, Width: s.Width, Height: s.Height,
				Profile: s.Profile, PixelFmt: s.PixFmt, HDRFormat: hdrFormat(s.ColorTransfer, s.Profile),
			}
		case "audio":
			info.Audio = append(info.Audio, AudioTrack{
				Index: s.Index, Codec: s.CodecName, Channels: s.Channels,
				Language: strings.ToLower(s.Tags.Language), Title: s.Tags.Title,
				Default: s.Disposition.Default == 1,
			})
		case "subtitle":
			info.Subtitles = append(info.Subtitles, SubtitleTrack{
				Index: s.Index, Codec: s.CodecName,
				Language: strings.ToLower(s.Tags.Language), Forced: s.Disposition.Forced == 1,
			})
		}
	}
	return info, nil
}

// primaryFormat reduces ffprobe's comma-joined format list to the one that
// matters. It reports "matroska,webm" for every Matroska file, and a decision
// made on the whole string matches nothing.
func primaryFormat(name string) string {
	if i := strings.IndexByte(name, ','); i >= 0 {
		return name[:i]
	}
	return name
}

// hdrFormat names the release's dynamic range from its transfer characteristic.
// It is best-effort and deliberately shallow: distinguishing HDR10 from HDR10+
// or Dolby Vision needs side-data ffprobe only reports with more flags, and
// nothing consumes the distinction yet.
func hdrFormat(transfer, profile string) string {
	switch strings.ToLower(transfer) {
	case "smpte2084":
		return "HDR10"
	case "arib-std-b67":
		return "HLG"
	}
	if strings.Contains(strings.ToLower(profile), "dolby vision") {
		return "DolbyVision"
	}
	return ""
}
