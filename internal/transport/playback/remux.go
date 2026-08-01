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
	args = append(args, reconnectArgs(upstreamURL)...)
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	if offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64), "-copyts")
	}
	args = append(args, "-i", upstreamURL)
	args = append(args, plan.ffmpegArgs(upstreamURL)...)
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

// reconnectArgs lets ffmpeg re-establish a dropped upstream connection instead
// of ending the transcode.
//
// **The remux path holds one HTTP connection open for the length of a film.** A
// debrid CDN closing it after ninety minutes is ordinary rather than
// exceptional, and without these the transcode simply ends: the spool stops
// growing, readers see a clean EOF, and the viewer gets a stream that stops
// mid-scene with nothing anywhere reporting an error. The relay path already
// survives this, because a media element re-requests a range and the origin
// opens a fresh upstream request for it.
//
// Only for http and https, and that guard is load-bearing rather than tidiness:
// these are options of the HTTP protocol, and ffmpeg exits with "Option
// reconnect not found" when nothing consumes one. A module resolving to any
// other scheme must not be made unplayable by a flag meant to make it more
// reliable.
//
// **`-reconnect_at_eof` is deliberately not among them**, though the reference
// implementations pass it. It treats end-of-file as an error worth reconnecting
// over, which is right for a live stream and wrong for a film: these inputs are
// finite and range-capable, so ffmpeg knows the real length, and reconnecting at
// a legitimate end would turn the last frame into a retry loop.
func reconnectArgs(upstreamURL string) []string {
	lower := strings.ToLower(upstreamURL)
	if !strings.HasPrefix(lower, "http://") && !strings.HasPrefix(lower, "https://") {
		return nil
	}
	return []string{
		"-reconnect", "1",
		// Streamed responses too: the origin asks for a range and reads it to the
		// end, which ffmpeg treats as non-seekable once it is reading.
		"-reconnect_streamed", "1",
		// Bounded backoff. A source that is genuinely gone should fail the play
		// rather than retry behind a viewer watching a still frame.
		"-reconnect_delay_max", "5",
	}
}

// Segment starts a transcode that writes HLS segments into dir, beginning at
// segment index n (ADR 0109, ADR 0111).
//
// It returns handles to stop the process and to pause and resume it. The last
// two are what the throttle needs: an audio-only remux runs at near-copy speed,
// so without a way to hold it back one playback writes the whole release to
// disk as fast as the upstream serves it.
//
// Three flags carry the design and each is load-bearing:
//
//   - **`-ss` before `-i`, with `-copyts`** — ADR 0108's arithmetic, re-keyed
//     from a byte offset to a segment index. Together they make segment n begin
//     at the timestamp its nominal position implies rather than at zero, which
//     is what lets a client seek into a stream nothing has produced yet.
//   - **`-start_number n`** names the output for its position in the *release*
//     rather than its position in this run, so a restart mid-film writes the
//     segment the client actually asked for.
//   - **`-hls_flags temp_file`** writes each segment under a temporary name and
//     renames it, so a file appearing at its final name means a finished
//     segment. Presence is the readiness signal, and without this flag it would
//     be a race — verified: a run leaves no `.tmp` behind.
func (r *Remuxer) Segment(ctx context.Context, upstreamURL string, headers map[string]string, plan Plan, dir string, length time.Duration, n int) (stop, pause, resume func(), err error) {
	if !r.Available() {
		return nil, nil, nil, ErrRemuxUnavailable
	}

	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, reconnectArgs(upstreamURL)...)
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	if offset := segmentStart(n, length); offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64))
	}
	// -copyts unconditionally, unlike the pipe path, because here it is what
	// makes a restarted segment carry the timestamps its index promises. Segment
	// zero of a restart at zero is unaffected.
	args = append(args, "-copyts", "-i", upstreamURL)
	args = append(args, plan.ffmpegArgs(upstreamURL)...)
	// Where the video is re-encoded the origin places the keyframes, so the
	// boundaries are exactly the nominal grid. Where it is copied this does
	// nothing and the source's own keyframes decide — which the nominal grid
	// tolerates, because a seek restarts rather than accumulating the difference.
	if plan.Video == ActionEncode || plan.Burn != nil {
		args = append(args, "-force_key_frames",
			"expr:gte(t,n_forced*"+strconv.FormatFloat(length.Seconds(), 'f', 3, 64)+")")
	}
	args = append(args,
		"-f", "hls",
		"-hls_time", strconv.FormatFloat(length.Seconds(), 'f', 3, 64),
		"-hls_flags", "temp_file",
		"-hls_segment_type", "fmp4",
		"-hls_fmp4_init_filename", segmentName(initSegment),
		"-hls_segment_filename", path.Join(dir, "%d.m4s"),
		"-start_number", strconv.Itoa(n),
		"-hls_playlist_type", "vod",
		"-hls_list_size", "0",
		// ffmpeg's own playlist is written and never served: the origin computes
		// its own from the probed duration, because ffmpeg's describes only what
		// this run emitted and a client needs the whole release up front.
		path.Join(dir, "ffmpeg.m3u8"),
	)

	cmd := exec.CommandContext(ctx, r.binary, args...)
	if err := cmd.Start(); err != nil {
		cancel()
		return nil, nil, nil, err
	}

	proc := cmd.Process
	stop = func() {
		cancel()
		_ = cmd.Wait()
	}
	pause = func() { signalProcess(proc, pauseSignal) }
	resume = func() { signalProcess(proc, resumeSignal) }
	return stop, pause, resume, nil
}

// Subtitles extracts one window of one embedded subtitle track as WebVTT
// (ADR 0113).
//
// It is the same arithmetic the video segments use — a start derived from an
// index, a length, `-copyts` so the cues carry the times the source gave them —
// applied to a stream that costs almost nothing to produce and quite a lot to
// reach. **The cost is the container, not the text.** Subtitle packets are
// interleaved with the video, so getting a minute of dialogue means reading a
// minute of pictures, and `-ss` before `-i` is what keeps that to a range
// request over the window rather than a read from the beginning of the release.
//
// `-vn -an` drop the video and audio outputs. They do not stop ffmpeg reading
// the container, and nothing can; what they stop is it spending any time on
// frames nobody will see.
//
// The result is small enough — a minute of dialogue is a few hundred bytes — to
// go straight to the response, which is why nothing here writes a file. The
// segment spool exists because a video segment must be complete before it is
// served; a WebVTT segment has no such constraint, so the honest thing is to not
// invent a spool for it.
func (r *Remuxer) Subtitles(ctx context.Context, upstreamURL string, headers map[string]string, index, n int) (io.ReadCloser, func(), error) {
	if !r.Available() {
		return nil, nil, ErrRemuxUnavailable
	}

	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, reconnectArgs(upstreamURL)...)
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	if offset := segmentStart(n, subtitleWindow); offset > 0 {
		args = append(args, "-ss", strconv.FormatFloat(offset.Seconds(), 'f', 3, 64))
	}
	// **`-to`, not `-t`, and the difference is not stylistic.** `-t` bounds the
	// output's *duration*, which `-copyts` has already rebased onto the source's
	// clock — so a window starting at 60 s with `-t 60` stops the instant it
	// starts and every window but the first comes out empty. Measured: window 0
	// yielded six cues and window 1 yielded none. `-to` is the absolute form and
	// is the one that composes with `-copyts`.
	args = append(args, "-copyts", "-i", upstreamURL,
		"-to", strconv.FormatFloat(segmentStart(n+1, subtitleWindow).Seconds(), 'f', 3, 64),
		"-vn", "-an",
		"-map", fmt.Sprintf("0:%d", index),
		"-c:s", "webvtt", "-f", "webvtt", "pipe:1")

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

// StyledSubtitles extracts one subtitle track as the script it was authored as
// (ADR 0115).
//
// **It is not windowed, and it cannot be.** An ASS script is one document: a
// header, a style table and then the events, and libass needs all of it before
// it can draw the first line. So this reads the container from the start, which
// is the cost of typeset fidelity — the same read burning would have done, minus
// the encode.
//
// The output is small enough to stream straight to the response; a two-hour
// film's script is a few hundred kilobytes. Nothing is written to disk.
func (r *Remuxer) StyledSubtitles(ctx context.Context, upstreamURL string, headers map[string]string, ordinal int) (io.ReadCloser, func(), error) {
	if !r.Available() {
		return nil, nil, ErrRemuxUnavailable
	}

	ctx, cancel := context.WithCancel(ctx)
	args := []string{"-hide_banner", "-loglevel", "error"}
	args = append(args, reconnectArgs(upstreamURL)...)
	if h := ffmpegHeaderArg(headers); h != "" {
		args = append(args, "-headers", h)
	}
	// No -ss and no -copyts. The script's own timestamps are what libass draws
	// against and the client aligns them to the media clock itself, so seeking
	// the input here would only lose the events before the offset.
	args = append(args, "-i", upstreamURL, "-vn", "-an",
		"-map", fmt.Sprintf("0:s:%d", ordinal), "-c:s", "copy", "-f", "ass", "pipe:1")

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
func serveRemuxed(w http.ResponseWriter, r *http.Request, t ticket, plan Plan, rx *Remuxer) {
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
func (p Plan) ffmpegArgs(upstreamURL string) []string {
	var args []string
	switch {
	case p.Burn != nil && p.Burn.Graphic:
		// A picture track is composited onto the frames, reading the stream this
		// run already has open — so no filename, no escaping and no second read
		// of the source. eof_action=pass keeps the video running past the last
		// subtitle rather than ending with it, and repeatlast=0 stops the final
		// caption being held on screen for the rest of the film.
		graph := fmt.Sprintf("[0:%d]scale[sub];[0:%d]", p.Burn.Index, p.VideoIndex)
		if vf := p.videoFilter(); vf != "" {
			graph += vf + ","
		}
		graph += "null[main];[main][sub]overlay=eof_action=pass:repeatlast=0[v]"
		args = append(args, "-filter_complex", graph, "-map", "[v]")
	case p.Burn != nil:
		// A text track is drawn by libass, which reads a *file* — so the source
		// is named again and opened a second time. That is the cost of typeset
		// fidelity and there is no way around it: the filter cannot be handed a
		// stream that is already open.
		chain := p.videoFilter()
		if chain != "" {
			chain += ","
		}
		chain += fmt.Sprintf("subtitles=filename=%s:si=%d",
			escapeFilterValue(upstreamURL), p.Burn.Ordinal)
		args = append(args, "-map", fmt.Sprintf("0:%d", p.VideoIndex), "-vf", chain)
	default:
		args = append(args, "-map", fmt.Sprintf("0:%d", p.VideoIndex))
		if p.Video == ActionEncode {
			if vf := p.videoFilter(); vf != "" {
				args = append(args, "-vf", vf)
			}
		}
	}

	if p.Video == ActionEncode || p.Burn != nil {
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

// serveSegmented answers the HLS surface for a release that must go through
// ffmpeg (ADR 0109, ADR 0111).
//
// Three resources and a closed set: the playlist, the initialisation segment,
// and a numbered segment. The resource is matched rather than used as a
// filename — it arrives in a URL, and the one thing a path from a URL must never
// be is a path.
func serveSegmented(w http.ResponseWriter, r *http.Request, rx *Remuxer, sessions *SegmentSessions, t ticket, key, resource string) {
	// Without a duration there is nothing to divide into segments, so the origin
	// serves the old unseekable pipe and says so. That case is real — a source
	// that reports no running time — and it is better than synthesising a
	// timeline and sending a player to a position that does not exist.
	if t.Plan.Duration <= 0 || !sessions.usable() {
		serveRemuxed(w, r, t, t.Plan, rx)
		return
	}

	length := segmentLengthFor(t.Plan)

	switch {
	// The entry point is a master when there are subtitles to declare and the
	// media playlist itself when there are not, so a release with none is served
	// exactly what it was before subtitles existed — no extra round trip bought
	// for a rendition list that would be empty (ADR 0113).
	case (resource == "" || resource == PlaylistName) && len(t.Plan.Subtitles) > 0:
		writePlaylist(w, r, masterPlaylist(
			masterBandwidth(t.Plan.SourceBytes, t.Plan.Duration), t.Plan.Subtitles))

	case resource == "" || resource == PlaylistName || resource == videoPlaylistName:
		// The playlist is derived from the probed duration and nothing else, so
		// it is identical on every request for this ticket and costs nothing to
		// re-render. It is still not cached: the ticket behind it expires.
		writePlaylist(w, r, mediaPlaylist(t.Plan.Duration, length, func(n int) string {
			return segmentName(n)
		}))

	case strings.HasSuffix(resource, ".m3u8"):
		i, ok := subtitlePlaylistOf(resource)
		if !ok || i >= len(t.Plan.Subtitles) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		writePlaylist(w, r, subtitlePlaylist(t.Plan.Duration, i))

	case strings.HasSuffix(resource, ".ass"):
		i, ok := styledSubtitleOf(resource)
		if !ok || i >= len(t.Plan.Styled) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		serveStyledSubtitle(w, r, rx, t, t.Plan.Styled[i].Ordinal)

	case strings.HasSuffix(resource, ".vtt"):
		i, n, ok := subtitleSegmentOf(resource)
		if !ok || i >= len(t.Plan.Subtitles) || n >= segmentCount(t.Plan.Duration, subtitleWindow) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		serveSubtitleSegment(w, r, rx, t, t.Plan.Subtitles[i].Index, n)

	case resource == segmentName(initSegment):
		body, size, err := sessions.OpenInit(r.Context(), rx, key, t.URL, t.Headers, t.Plan, length)
		if err != nil {
			http.Error(w, "the origin could not produce a playable stream for this release ("+t.Plan.Reason+")", http.StatusBadGateway)
			return
		}
		defer body.Close()
		writeSegment(w, r, "video/mp4", size, body)

	default:
		n, ok := segmentIndexOf(resource)
		if !ok {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if n >= segmentCount(t.Plan.Duration, length) {
			// Past the end of the release. A player should never ask, since the
			// playlist it was given closes with ENDLIST, so this is a malformed
			// request rather than a race.
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		body, size, err := sessions.Open(r.Context(), rx, key, t.URL, t.Headers, t.Plan, length, n)
		if err != nil {
			http.Error(w, "the origin could not produce a playable stream for this release ("+t.Plan.Reason+")", http.StatusBadGateway)
			return
		}
		defer body.Close()
		writeSegment(w, r, "video/iso.segment", size, body)
	}
}

// PlaylistName is the playlist a client is pointed at. What it holds depends on
// the release: the video's own segment list when there are no subtitles, and a
// master declaring the video and each subtitle rendition when there are
// (ADR 0113). The client asks for the same URL either way.
//
// Exported because the transport that mints a ticket has to point the client at
// it, and a second spelling of the same path is exactly the drift this avoids.
const PlaylistName = "index.m3u8"

// writePlaylist sends a playlist of either kind. Nothing here is cached: the
// ticket behind it expires, and a playlist that outlived its ticket would point
// a player at resources it can no longer fetch.
func writePlaylist(w http.ResponseWriter, r *http.Request, body string) {
	w.Header().Set("Content-Type", mediaPlaylistType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.WriteString(w, body)
}

// serveStyledSubtitle sends one subtitle track as the script it was authored as.
//
// Unlike a WebVTT window, an empty result here is a failure rather than a quiet
// nothing: a script with no events is a script libass will draw nothing from for
// the whole film, and the client has a flattened rendition to fall back to only
// if it is told this one did not work. So the first bytes are read before the
// status is written, the same ordering serveRemuxed uses and for the same
// reason.
func serveStyledSubtitle(w http.ResponseWriter, r *http.Request, rx *Remuxer, t ticket, ordinal int) {
	body, stop, err := rx.StyledSubtitles(r.Context(), t.URL, t.Headers, ordinal)
	if err != nil {
		http.Error(w, "subtitles unavailable", http.StatusBadGateway)
		return
	}
	defer stop()
	defer body.Close()

	first := make([]byte, 4096)
	n, readErr := io.ReadFull(body, first)
	if n == 0 {
		http.Error(w, "the origin could not extract this subtitle track", http.StatusBadGateway)
		return
	}

	w.Header().Set("Content-Type", assMimeType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := w.Write(first[:n]); err != nil {
		return
	}
	if readErr == io.ErrUnexpectedEOF || readErr == io.EOF {
		return
	}
	_, _ = io.Copy(w, body)
}

// serveSubtitleSegment extracts and sends one window of one subtitle track.
//
// It streams rather than buffering, and answers 200 with no Content-Length: the
// output is a few hundred bytes and its size is not known until ffmpeg has
// finished, so buffering to learn a number nobody needs would only add latency.
//
// **An extraction that produces nothing is a success, not an error.** A minute
// of a film with no dialogue in it is an ordinary thing, and the WebVTT header
// alone is a valid document meaning exactly that. Only a failure to start ffmpeg
// is a failure, and it is the one case where a player showing no subtitles for
// the rest of the film should instead be told the origin could not produce them.
func serveSubtitleSegment(w http.ResponseWriter, r *http.Request, rx *Remuxer, t ticket, index, n int) {
	body, stop, err := rx.Subtitles(r.Context(), t.URL, t.Headers, index, n)
	if err != nil {
		http.Error(w, "subtitles unavailable", http.StatusBadGateway)
		return
	}
	defer stop()
	defer body.Close()

	w.Header().Set("Content-Type", vttMimeType)
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	if _, err := io.Copy(w, body); err != nil {
		return
	}
}

// segmentLengthFor is how long a segment runs for this plan.
//
// The same value either way, and the reason differs. Where the video is
// re-encoded the origin places the keyframes with -force_key_frames, so the
// boundaries are exactly this. Where it is copied the source's keyframes decide
// and this is only what ffmpeg is asked for — which the nominal grid tolerates,
// because a seek restarts at the position the playlist names rather than
// inheriting where the previous segment happened to end (ADR 0111).
func segmentLengthFor(plan Plan) time.Duration { return encodedSegmentLength }

// segmentIndexOf reads a segment index out of a resource name, accepting only
// the exact form the playlist emits.
func segmentIndexOf(resource string) (int, bool) {
	name, ok := strings.CutSuffix(resource, ".m4s")
	if !ok || name == "" {
		return 0, false
	}
	n, err := strconv.Atoi(name)
	if err != nil || n < 0 || strconv.Itoa(n) != name {
		return 0, false
	}
	return n, true
}

// writeSegment sends one segment with a real length, which a segment always has
// because it is a finished file rather than a live pipe. That is the difference
// the whole slice bought: the byte-addressed origin advertised a length it had
// to estimate, and could not make the estimate true.
func writeSegment(w http.ResponseWriter, r *http.Request, contentType string, size int64, body io.Reader) {
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.FormatInt(size, 10))
	// A segment is immutable for the life of the ticket, but the ticket is
	// short-lived and the upstream behind it shorter still, so nothing here is
	// worth a cache that could outlive what it points at.
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, body)
}
