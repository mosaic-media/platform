// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os/exec"
	"path"
	"strings"
)

// Stream-copy remux (ADR 0048's named next step after selection).
//
// The constraint it answers is narrow and hard: Media Source Extensions — how
// every browser plays adaptive video — accepts only fragmented MP4 and WebM.
// **Matroska cannot pass through it whatever codec is inside**, so a 1080p h264
// release in an MKV is exactly as unplayable in a browser as an HEVC one. That
// is a *container* problem, and a container problem has a cheap answer: rewrite
// the container and copy the streams through untouched. No decoding, no
// encoding, near-zero CPU.
//
// Two things it deliberately does not do, because stream copy cannot:
//
//   - It does not fix codecs. h264+AAC in an MKV becomes playable; h264+EAC3
//     becomes a playable container the browser still cannot decode the audio of.
//     That needs real transcoding, which stays deferred.
//   - It does not make the stream seekable. Fragmented MP4 off a pipe has no
//     index and no length, so the origin cannot answer a Range request over it
//     and a player treats it as an unbounded stream. Seeking needs either HLS
//     segmenting or restarting ffmpeg at an offset; both are follow-ups, and
//     neither is pretended at here.
//
// It lives on the Platform rather than in the playback module on purpose. A
// module *resolves* and never serves (ADR 0045); a remux is a transform on the
// serving side, so putting it in a module would hand a module the byte path the
// whole contract keeps away from it — and would put an ffmpeg dependency behind
// the SDK boundary.

// remuxContainers are the container extensions MSE cannot accept and stream
// copy can rescue. It is matched on the upstream path, which is a heuristic:
// a URL need not carry a truthful extension. It is the cheap signal available
// before ADR 0048's probe exists, and it fails safe — a mislabelled file is
// relayed unchanged, exactly as today.
var remuxContainers = map[string]bool{
	".mkv":  true,
	".avi":  true,
	".ts":   true,
	".m2ts": true,
	".wmv":  true,
	".flv":  true,
	".mov":  false, // QuickTime is already an MP4 family container; MSE takes it.
}

// ShouldRemux reports whether an upstream location needs its container
// rewritten before a browser can play it.
//
// The decision is made when the ticket is minted, not when bytes are fetched,
// so it sits with the server-side knowledge that will grow into ADR 0048's
// profile-driven selection rather than being re-derived per range request.
func ShouldRemux(upstreamURL string) bool {
	i := strings.IndexAny(upstreamURL, "?#")
	clean := upstreamURL
	if i >= 0 {
		clean = upstreamURL[:i]
	}
	return remuxContainers[strings.ToLower(path.Ext(clean))]
}

// ErrRemuxUnavailable reports that a remux was asked for and ffmpeg is not
// installed. It is a distinct error because the answer for a user is specific —
// install ffmpeg, or pick a different release — rather than "playback failed".
var ErrRemuxUnavailable = errors.New("playback: ffmpeg is not available to remux this container")

// Remuxer rewrites a stream's container on the way through, copying the codec
// streams untouched.
type Remuxer struct {
	// binary is the resolved ffmpeg path, empty when none was found.
	binary string
}

// NewRemuxer looks for ffmpeg on PATH. A Remuxer with no binary is valid and
// reports Available() false: remux is an enhancement, and the Platform must
// still boot and direct-play without it.
func NewRemuxer() *Remuxer {
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		return &Remuxer{}
	}
	return &Remuxer{binary: bin}
}

// NewRemuxerAt builds a Remuxer over an explicit binary path, for a deployment
// that ships ffmpeg somewhere other than PATH, and for tests.
func NewRemuxerAt(binary string) *Remuxer { return &Remuxer{binary: binary} }

// Available reports whether a remux can actually be performed.
func (r *Remuxer) Available() bool { return r != nil && r.binary != "" }

// ContentType is the media type a remuxed stream is served as.
func (r *Remuxer) ContentType() string { return "video/mp4" }

// Stream starts ffmpeg against upstreamURL and returns its stdout, carrying out
// plan. The caller reads to completion (or closes) and the returned cancel func
// must be called to reap the process — a reader that goes away without it leaves
// ffmpeg pulling bytes from the upstream forever.
func (r *Remuxer) Stream(ctx context.Context, upstreamURL string, headers map[string]string, plan Plan) (io.ReadCloser, func(), error) {
	if !r.Available() {
		return nil, nil, ErrRemuxUnavailable
	}

	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error"}
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	args = append(args, "-i", upstreamURL)
	args = append(args, plan.ffmpegArgs()...)
	args = append(args,
		// Fragmented output, written without seeking back to patch a header —
		// which is what makes it streamable down a pipe at all.
		"-movflags", "frag_keyframe+empty_moov+default_base_moof",
		"-f", "mp4", "pipe:1",
	)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, nil, err
	}
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, err
	}

	stop := func() {
		cancel()
		_ = cmd.Wait()
	}
	return stdout, stop, nil
}

// ffmpegHeaderArg renders request headers in the CRLF-delimited form ffmpeg's
// -headers flag expects, so a credentialed upstream is reachable by the remux
// path on the same terms as the relay path.
func ffmpegHeaderArg(headers map[string]string) string {
	if len(headers) == 0 {
		return ""
	}
	var b strings.Builder
	for k, v := range headers {
		b.WriteString(k)
		b.WriteString(": ")
		b.WriteString(v)
		b.WriteString("\r\n")
	}
	return b.String()
}

// serveRemuxed pipes a container-rewritten stream to the client.
//
// It answers 200 and never 206: there is no index and no length, so a Range
// request cannot be honoured. Saying so with Accept-Ranges: none is the honest
// signal — a player that asks for a byte range gets told the source does not do
// them, rather than being handed a wrong answer.
func serveRemuxed(w http.ResponseWriter, r *http.Request, rx *Remuxer, t ticket, plan Plan) {
	body, stop, err := rx.Stream(r.Context(), t.URL, t.Headers, plan)
	if err != nil {
		http.Error(w, "remux unavailable", http.StatusBadGateway)
		return
	}
	defer stop()
	defer body.Close()

	w.Header().Set("Content-Type", rx.ContentType())
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, body)
}

// ffmpegArgs renders a Plan as ffmpeg stream mapping and codec flags.
//
// The whole point is the asymmetry: video is copied wherever it can be, because
// re-encoding a 4K HDR stream is the one operation that will not keep up on a
// home server, while an audio encode is cheap enough to be unremarkable. The
// chosen audio track is named by index rather than taken as "the first one",
// which is how a release whose first track is Hindi ends up playing English.
func (p Plan) ffmpegArgs() []string {
	args := []string{"-map", fmt.Sprintf("0:%d", p.VideoIndex)}
	if p.Video == ActionEncode {
		// Only reached when the client cannot decode the source video at all.
		// veryfast because a slower preset on a 4K source is not a trade a
		// viewer waiting for a first frame would accept.
		args = append(args, "-c:v", "libx264", "-preset", "veryfast", "-crf", "20")
	} else {
		args = append(args, "-c:v", "copy")
	}

	switch p.Audio {
	case ActionCopy:
		args = append(args, "-map", fmt.Sprintf("0:%d", p.AudioIndex), "-c:a", "copy")
	case ActionEncode:
		// Downmixed to stereo deliberately: a browser is overwhelmingly on
		// stereo output, and a 5.1 AAC track it renders as two channels loses
		// the centre dialogue that matters most.
		args = append(args, "-map", fmt.Sprintf("0:%d", p.AudioIndex),
			"-c:a", "aac", "-b:a", "192k", "-ac", "2")
	case ActionDrop:
		args = append(args, "-an")
	}

	// Subtitles never travel in the muxed output. An MKV's are usually SubRip or
	// ASS, neither of which maps into MP4, and copying them in fails the whole
	// command. They are resolved as separate tracks instead (ADR 0037).
	args = append(args, "-sn")
	return args
}
