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
	"strconv"
	"strings"
	"time"
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
	return r.StreamFrom(ctx, upstreamURL, headers, plan, 0)
}

// StreamFrom is Stream starting at an offset into the release, which is what
// makes a transcoded stream seekable (ADR 0108).
//
// Two flags carry the whole idea and both are load-bearing:
//
//   - **`-ss` before `-i`** seeks the *input*. ffmpeg then asks the upstream for
//     a byte range rather than decoding and discarding everything before the
//     offset, which is only affordable because the upstream honours Range — the
//     measurement this design rests on. After `-i` it would decode from zero.
//   - **`-copyts`** keeps the source's timestamps instead of rebasing them to
//     zero. Without it every seek produces a stream that claims to start at 0,
//     so the player's clock jumps back to the beginning and the scrubber fights
//     the viewer. With it the fragments carry the real decode times and the
//     player lands where the person asked to be.
//
// An offset of zero is an ordinary play from the start and adds neither flag,
// so the un-seeked path is byte-for-byte what it was.
func (r *Remuxer) StreamFrom(ctx context.Context, upstreamURL string, headers map[string]string, plan Plan, offset time.Duration) (io.ReadCloser, func(), error) {
	if !r.Available() {
		return nil, nil, ErrRemuxUnavailable
	}

	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error"}
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	if offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64), "-copyts")
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
func serveRemuxed(w http.ResponseWriter, r *http.Request, rx *Remuxer, sessions *Sessions, t ticket, plan Plan, key string) {
	// A seekable plan answers ranges out of a single transcode's file; one with
	// no duration cannot map an offset to a time and falls back to the pipe
	// below, honestly labelled.
	if plan.seekable() && sessions.usable() {
		serveSeekableRemux(w, r, rx, sessions, t, plan, key)
		return
	}

	body, stop, err := rx.Stream(r.Context(), t.URL, t.Headers, plan)
	if err != nil {
		http.Error(w, "remux unavailable", http.StatusBadGateway)
		return
	}
	defer stop()
	defer body.Close()

	// **The first bytes are read before the status is written**, and that
	// ordering is the whole point of this block.
	//
	// Writing 200 first means an ffmpeg that dies on its own arguments still
	// produces a successful response with an empty body. The player then reports
	// only "format not supported" and the access log says status=200, so the one
	// place that knows what went wrong — ffmpeg's stderr — is the one place
	// nobody is looking. That is exactly how a FLAC track the MP4 muxer refuses
	// presented as a broken file for a whole session.
	//
	// A remux that has produced its first bytes has written a valid fMP4 header,
	// so this is a real signal rather than a delay: past it, failures are
	// mid-stream ones a status code could not have described anyway.
	first := make([]byte, 64*1024)
	n, readErr := io.ReadFull(body, first)
	if n == 0 {
		http.Error(w, "the origin could not produce a playable stream for this release ("+plan.Reason+")", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", rx.ContentType())
	w.Header().Set("Accept-Ranges", "none")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(first[:n]); err != nil {
		return
	}
	// ErrUnexpectedEOF means the whole stream was shorter than the probe buffer,
	// which is legitimate for a very short output and is already fully written.
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
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
		if vf := p.videoFilter(); vf != "" {
			args = append(args, "-vf", vf)
		}
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

// videoFilter builds the scale and tone-map chain for an encoded video stream.
//
// Downscaling comes first, and deliberately: tone-mapping is per-pixel, so doing
// it after a 4K-to-1080p reduction is roughly a quarter of the work for a result
// nobody can tell apart in a browser window.
//
// The tone-map chain converts to linear light, maps into BT.709 with Hable, and
// converts back — the standard sequence, spelled out rather than hidden behind a
// preset because each step is doing something and a missing one shows up as
// washed-out or oversaturated colour rather than an error.
func (p Plan) videoFilter() string {
	var parts []string
	if p.MaxHeight > 0 {
		// -2 keeps the width even, which h264 requires, and `min` leaves a
		// source already below the cap untouched rather than upscaling it.
		parts = append(parts, fmt.Sprintf("scale=-2:'min(ih,%d)'", p.MaxHeight))
	}
	if p.Tonemap {
		parts = append(parts,
			"zscale=t=linear:npl=100",
			"format=gbrpf32le",
			"zscale=p=bt709",
			"tonemap=tonemap=hable:desat=0",
			"zscale=t=bt709:m=bt709:r=tv",
		)
	}
	if len(parts) == 0 {
		return ""
	}
	// yuv420p last: it is what every browser decoder expects, and the tone-map
	// chain leaves the frames in a float format nothing else accepts.
	parts = append(parts, "format=yuv420p")
	return strings.Join(parts, ",")
}

// Seekable transcoded output (ADR 0108).
//
// A bare <video> is the whole constraint. It has no MSE and no HLS library
// (ADR 0070), so the only way it knows how to seek is to ask for a **byte**
// range — and a live transcode has no byte index to answer one with. What it
// does have, once the probe reports a duration, is a defensible *estimate*: the
// output is roughly constant-bitrate over its length, so an offset is a
// position in time.
//
// So the origin advertises a synthetic length, converts a requested offset into
// a timestamp, and restarts ffmpeg there with -copyts so the fragments carry
// real decode times. The player's clock then lands where the viewer asked, and
// the estimate only ever affects where the *scrubber handle* sits — never which
// frame is shown, because that comes from the timestamps rather than from this
// arithmetic.
//
// **The estimate is honest about being one.** Its error is a variable-bitrate
// release whose scrubber is slightly non-linear: dragging to the middle lands
// near the middle rather than exactly on it. That is the cost of seeking at all
// on a player that cannot be told anything else, and it is bounded — every seek
// is served from a real keyframe, so nothing lands in the middle of a fragment.

// estimatedBitrate is the assumed output rate for a transcoded stream, in bytes
// per second, used only to synthesise a Content-Length.
//
// 2 MB/s ≈ 16 Mbps, which is a generous 1080p h264 with stereo AAC — the shape
// this origin actually emits, since MaxHeight bounds the encode. Over-estimating
// is the safe direction: the advertised length is longer than the real output,
// so the player never asks for a byte past the end of what ffmpeg will produce.
// Under-estimating would truncate the timeline and make the last minutes
// unreachable.
const estimatedBitrate = 2 << 20

// seekable reports whether a plan can answer range requests, which needs a
// duration to map offsets onto. Without one the origin serves the honest pipe.
func (p Plan) seekable() bool { return p.Duration > 0 }

// contentLength is the synthetic total the origin advertises.
func (p Plan) contentLength() int64 {
	return int64(p.Duration.Seconds() * estimatedBitrate)
}

// offsetAt converts a byte offset in the advertised stream to a position in the
// release. It clamps rather than extrapolating: a player asking past the end
// gets the end, not a negative seek or a timestamp beyond the film.
func (p Plan) offsetAt(byteOffset int64) time.Duration {
	total := p.contentLength()
	if byteOffset <= 0 || total <= 0 {
		return 0
	}
	if byteOffset >= total {
		return p.Duration
	}
	return time.Duration(float64(p.Duration) * (float64(byteOffset) / float64(total)))
}

// parseByteRangeStart reads the first byte position out of a Range header.
//
// Only `bytes=N-` and `bytes=N-M` are understood, which is what a media element
// sends. A suffix range (`bytes=-N`, "the last N bytes") is deliberately not
// handled: it asks for the end of a stream whose real length is unknown, and
// answering it with an estimate would seek to a position that may not exist.
// Reporting "not satisfiable" for it is the honest answer.
func parseByteRangeStart(header string) (int64, bool) {
	const prefix = "bytes="
	if !strings.HasPrefix(header, prefix) {
		return 0, false
	}
	spec := strings.TrimPrefix(header, prefix)
	// One range only. A multi-range request over a live transcode has no sane
	// answer, and no media element sends one.
	if strings.Contains(spec, ",") {
		return 0, false
	}
	dash := strings.Index(spec, "-")
	if dash <= 0 {
		return 0, false
	}
	n, err := strconv.ParseInt(strings.TrimSpace(spec[:dash]), 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// serveSeekableRemux answers a transcoded stream as a byte-addressable resource.
//
// The shape is what a media element expects of any ordinary file: a length, an
// Accept-Ranges, and a 206 carrying a Content-Range when it asks for an offset.
// What differs is that the bytes are produced on demand from a timestamp rather
// than read out of one — so a seek costs a fresh ffmpeg at the mapped position,
// and the previous one is reaped when its reader goes away.
//
// **Every response is a fresh process, and that is the cost being accepted.** A
// viewer dragging the scrubber across a film starts several transcodes in a few
// seconds. Bounding that is a session concern rather than a correctness one —
// the reference implementations keep a keyed session and kill the running
// process on a new seek, and the same is owed here — but each request in
// isolation is correct, which is the property this builds first.
func serveSeekableRemux(w http.ResponseWriter, r *http.Request, rx *Remuxer, sessions *Sessions, t ticket, plan Plan, key string) {
	total := plan.contentLength()

	w.Header().Set("Content-Type", rx.ContentType())
	w.Header().Set("Accept-Ranges", "bytes")
	w.Header().Set("Cache-Control", "no-store")

	// A HEAD is how a media element learns the length before deciding it can
	// seek at all, so it must answer without starting a transcode.
	if r.Method == http.MethodHead {
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
		return
	}

	start, ranged := int64(0), false
	if h := r.Header.Get("Range"); h != "" {
		n, ok := parseByteRangeStart(h)
		if !ok || n >= total {
			// An unsatisfiable or unsupported range gets the status that says so,
			// with the length, rather than the whole stream from the beginning —
			// which would silently ignore the seek.
			w.Header().Set("Content-Range", "bytes */"+strconv.FormatInt(total, 10))
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		start, ranged = n, true
	}

	body, err := sessions.Open(r.Context(), rx, key, t.URL, t.Headers, plan, start)
	if err != nil {
		http.Error(w, "the origin could not produce a playable stream for this release ("+plan.Reason+")", http.StatusBadGateway)
		return
	}
	defer body.Close()

	// The same probe-before-status rule the pipe path follows: a transcode that
	// produces nothing must not read as a successful empty range.
	first := make([]byte, 64*1024)
	n, readErr := io.ReadFull(body, first)
	if n == 0 {
		http.Error(w, "the origin could not produce a playable stream for this release ("+plan.Reason+")", http.StatusBadGateway)
		return
	}

	// The advertised end is the estimate's end, not what ffmpeg will really
	// produce. It has to be: the player needs a range that closes, and the true
	// length is unknowable until the transcode finishes.
	if ranged {
		w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, total-1, total))
		w.Header().Set("Content-Length", strconv.FormatInt(total-start, 10))
		w.WriteHeader(http.StatusPartialContent)
	} else {
		w.Header().Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
	}

	if _, err := w.Write(first[:n]); err != nil {
		return
	}
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		return
	}
	_, _ = io.Copy(w, body)
}
