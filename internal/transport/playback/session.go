// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"context"
	"errors"
	"io"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/adapters/filesystem"
)

// Spool is the growable temporary file a session writes its transcode into and
// serves ranges out of. It is a port: the implementation is a filesystem
// adapter, because a transport may not open files directly (the Secret Broker's
// boundary rule) and because the segmenter's storage is infrastructure that
// belongs behind an interface rather than inline in a handler.
type Spool interface {
	WriteAt(p []byte, off int64) (int, error)
	ReadAt(p []byte, off int64) (int, error)
	Close() error
}

// NewSpool creates the backing store for one session. A registry built without
// one cannot serve seekable output and falls back to the pipe.
type NewSpool func() (Spool, error)

// The transcode session: one ffmpeg per playback, writing to a file that range
// requests are served out of.
//
// **This exists because the obvious design is wrong, and the way it is wrong is
// only visible in a browser.** Answering each range request with its own ffmpeg
// started at the mapped timestamp is correct per request and incoherent in
// aggregate: a media element issues overlapping, opportunistic ranges, so one
// playback drew bytes from two transcodes at different timestamps, the browser
// concatenated them into what it believed was one file, and the decoder failed.
// Six unit tests passed the whole time, because each response in isolation was
// right.
//
// The fix is that every reader sees the *same* bytes. One process writes to one
// file; a range is a read from that file, waiting when it asks for bytes the
// transcode has not reached yet. That is what `seanime`'s non-local path does,
// and ADR 0108 mistook it for a fallback for upstreams that cannot range — it is
// required by the *client's* range behaviour whatever the upstream supports.
//
// A genuine seek — an offset the current file does not and will not soon cover —
// restarts the transcode at the mapped time and rebases the file's byte zero
// onto that offset. The restart is affordable precisely because the upstream
// does honour Range, which is the part of ADR 0108's measurement that stands.

// readAheadSlack is how far past the written frontier a request may reach before
// it is treated as a seek rather than as a reader running ahead of the encoder.
//
// A player buffering forward asks for bytes that are moments away; a viewer
// dragging the scrubber asks for bytes that are minutes away. Without a
// threshold the first would restart the transcode continuously, which is the
// thrash this whole type exists to prevent. Eight megabytes is a few seconds of
// output — comfortably more than read-ahead and far less than any real seek.
const readAheadSlack = 8 << 20

// sessionIdle is how long a session outlives its last reader before it is
// reaped. It covers the gap between a player finishing one range and opening
// the next, which is routine and must not cost a restart.
const sessionIdle = 30 * time.Second

// ErrSessionSuperseded reports that the session a reader was following has been
// restarted at a different position. The reader stops rather than returning
// bytes from a file that no longer describes the offset it was serving.
var ErrSessionSuperseded = errors.New("playback: transcode session superseded by a seek")

// transcodeSession is one running ffmpeg and the file it is filling.
type transcodeSession struct {
	// origin is the advertised byte offset this file's byte zero corresponds to.
	// It is non-zero after a seek: the client's byte space is continuous, and
	// the file behind it starts again each time the transcode is restarted.
	origin int64

	mu      sync.Mutex
	cond    *sync.Cond
	spool   Spool
	written int64
	done    bool
	err     error

	stop     func()
	readers  int
	idleFrom time.Time
	used     time.Time
	dead     bool
}

// Sessions holds the running transcode per playback, keyed by ticket.
//
// One session per ticket is also what bounds the process fleet: before this, a
// viewer dragging the scrubber left an ffmpeg running per range request with
// nothing reaping the previous one.
// maxPerTicket bounds how many transcodes one playback may have running.
//
// **More than one is necessary, which the obvious design gets wrong.** A media
// element reads several regions of a file at once — the head for the header and,
// for a progressive MP4, the tail where a moov would normally live. Keeping a
// single session and killing it whenever a request falls outside it means the
// client's second region destroys the first, then the first destroys the second:
// live, one playback wrote 7.4 GB across two spools and the player never reached
// readyState 1, because nothing it had asked for survived long enough to be
// read.
//
// Three is enough for a head, a tail and a seek in flight, and small enough that
// a scrubbing viewer cannot accumulate processes — the least recently used is
// evicted, which is the bound that was owed.
const maxPerTicket = 3

type Sessions struct {
	mu    sync.Mutex
	by    map[string][]*transcodeSession
	spool NewSpool
}

// NewSessions builds a registry over a spool factory. A nil factory yields a
// registry that reports itself unusable, so a Platform assembled without one
// serves the pipe rather than failing a playback.
func NewSessions(spool NewSpool) *Sessions {
	return &Sessions{by: map[string][]*transcodeSession{}, spool: spool}
}

// usable reports whether this registry can back a seekable stream.
func (s *Sessions) usable() bool { return s != nil && s.spool != nil }

// Open returns a reader over the transcoded output positioned at the advertised
// byte offset, starting or restarting the transcode as needed.
//
// The returned reader follows the file as it grows, so a caller streams from it
// exactly as it would from a pipe — the difference is that a second caller
// asking for an overlapping range gets the same bytes rather than a second
// transcode's.
func (s *Sessions) Open(ctx context.Context, rx *Remuxer, key, url string, headers map[string]string, plan Plan, offset int64) (io.ReadCloser, error) {
	s.mu.Lock()
	live := s.by[key]

	// An existing session that already covers this offset is reused, whichever
	// region of the file it was started for. That is the whole point: two reads
	// of overlapping bytes must come from the same transcode.
	for _, sess := range live {
		if sess.covers(offset) {
			sess.touch()
			s.mu.Unlock()
			return sess.reader(offset)
		}
	}

	sess, err := startSession(ctx, rx, s.spool, url, headers, plan, offset)
	if err != nil {
		s.mu.Unlock()
		return nil, err
	}
	live = append(live, sess)

	// Evict the least recently used rather than the one just displaced: the
	// client is reading several regions and the oldest is the one it has
	// finished with.
	for len(live) > maxPerTicket {
		oldest, at := live[0], 0
		for i, c := range live[1:] {
			if c.lastUsed().Before(oldest.lastUsed()) {
				oldest, at = c, i+1
			}
		}
		oldest.kill()
		live = append(live[:at], live[at+1:]...)
	}
	s.by[key] = live
	s.mu.Unlock()

	return sess.reader(offset)
}

// Close stops every running transcode. It exists so a shutdown does not leave
// ffmpeg processes holding upstream connections open.
func (s *Sessions) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, live := range s.by {
		for _, sess := range live {
			sess.kill()
		}
		delete(s.by, k)
	}
}

// Reap stops sessions whose last reader left more than sessionIdle ago. A
// Platform calls it periodically; nothing else bounds a session whose viewer
// simply closed the tab, since there is no request in which to notice.
func (s *Sessions) Reap(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	reaped := 0
	for k, live := range s.by {
		kept := live[:0]
		for _, sess := range live {
			if sess.idle(now) {
				sess.kill()
				reaped++
				continue
			}
			kept = append(kept, sess)
		}
		if len(kept) == 0 {
			delete(s.by, k)
			continue
		}
		s.by[k] = kept
	}
	return reaped
}

// covers reports whether this session can serve the offset: at or after its
// origin, and not so far past what it has written that the request is a seek
// rather than a read-ahead.
func (t *transcodeSession) covers(offset int64) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dead || offset < t.origin {
		return false
	}
	// Once the transcode has finished, everything it produced is available and
	// nothing more will arrive, so the frontier is exact rather than advancing.
	if t.done {
		return offset <= t.origin+t.written
	}
	return offset <= t.origin+t.written+readAheadSlack
}

// touch and lastUsed order sessions for eviction. A session being read from is
// not a candidate however long ago it started.
func (t *transcodeSession) touch() {
	t.mu.Lock()
	t.used = time.Now()
	t.mu.Unlock()
}

func (t *transcodeSession) lastUsed() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.readers > 0 {
		return time.Now()
	}
	return t.used
}

func (t *transcodeSession) idle(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.readers == 0 && !t.idleFrom.IsZero() && now.Sub(t.idleFrom) > sessionIdle
}

func (t *transcodeSession) kill() {
	t.mu.Lock()
	if t.dead {
		t.mu.Unlock()
		return
	}
	t.dead = true
	sp := t.spool
	stop := t.stop
	t.cond.Broadcast() // wake readers so they can notice and stop
	t.mu.Unlock()

	if stop != nil {
		stop()
	}
	if sp != nil {
		_ = sp.Close()
	}
}

// startSession launches ffmpeg at the time this offset maps to and begins
// filling a file from its output.
func startSession(ctx context.Context, rx *Remuxer, newSpool NewSpool, url string, headers map[string]string, plan Plan, offset int64) (*transcodeSession, error) {
	sp, err := newSpool()
	if err != nil {
		return nil, err
	}

	// The transcode outlives the request that started it: a player closing one
	// range and opening the next must not kill the process between them.
	body, stop, err := rx.StreamFrom(context.WithoutCancel(ctx), url, headers, plan, plan.offsetAt(offset))
	if err != nil {
		_ = sp.Close()
		return nil, err
	}

	t := &transcodeSession{origin: offset, spool: sp, stop: stop, idleFrom: time.Now(), used: time.Now()}
	t.cond = sync.NewCond(&t.mu)

	go t.fill(body)
	return t, nil
}

// fill copies ffmpeg's output into the file, publishing the frontier as it goes
// so waiting readers wake as soon as their bytes exist.
func (t *transcodeSession) fill(body io.ReadCloser) {
	defer body.Close()
	buf := make([]byte, 256<<10)
	for {
		n, err := body.Read(buf)
		if n > 0 {
			t.mu.Lock()
			if t.dead {
				t.mu.Unlock()
				return
			}
			_, werr := t.spool.WriteAt(buf[:n], t.written)
			if werr != nil {
				t.err = werr
				t.done = true
				t.cond.Broadcast()
				t.mu.Unlock()
				return
			}
			t.written += int64(n)
			t.cond.Broadcast()
			t.mu.Unlock()
		}
		if err != nil {
			t.mu.Lock()
			if err != io.EOF {
				t.err = err
			}
			t.done = true
			t.cond.Broadcast()
			t.mu.Unlock()
			return
		}
	}
}

// reader opens an independent handle on the session's file positioned at the
// advertised offset. Each reader has its own handle because a browser reads
// several ranges at once and they must not share a file position.
func (t *transcodeSession) reader(offset int64) (io.ReadCloser, error) {
	t.mu.Lock()
	if t.dead {
		t.mu.Unlock()
		return nil, ErrSessionSuperseded
	}
	t.readers++
	t.idleFrom = time.Time{}
	t.mu.Unlock()

	// Every reader shares the spool and carries its own position, because a
	// browser reads several ranges at once and they must not share one cursor.
	return &sessionReader{sess: t, at: offset - t.origin}, nil
}

func (t *transcodeSession) release() {
	t.mu.Lock()
	t.readers--
	if t.readers <= 0 {
		t.readers = 0
		t.idleFrom = time.Now()
	}
	t.mu.Unlock()
}

// waitFor blocks until the file holds at least want bytes, the transcode ends,
// or the session is superseded.
func (t *transcodeSession) waitFor(want int64) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	for {
		switch {
		case t.dead:
			return ErrSessionSuperseded
		case t.written > want:
			return nil
		case t.done:
			if t.err != nil {
				return t.err
			}
			return io.EOF
		}
		t.cond.Wait()
	}
}

// sessionReader reads one range out of a session's file, following the
// transcode as it advances rather than stopping at the current end of file.
type sessionReader struct {
	sess *transcodeSession
	at   int64
	shut bool
}

func (r *sessionReader) Read(p []byte) (int, error) {
	for {
		if err := r.sess.waitFor(r.at); err != nil {
			// EOF here means the transcode finished and this reader has consumed
			// everything, which is an ordinary end of stream.
			return 0, err
		}
		n, err := r.sess.spool.ReadAt(p, r.at)
		if n > 0 {
			r.at += int64(n)
			return n, nil
		}
		if err != nil && err != io.EOF {
			return 0, err
		}
		// A short read at the frontier: the writer has advanced the counter but
		// the bytes are not visible to this handle yet. Wait and retry rather
		// than reporting an end of stream that has not happened.
	}
}

func (r *sessionReader) Close() error {
	if !r.shut {
		r.shut = true
		r.sess.release()
	}
	return nil
}

// DefaultSpool is the spool a Platform uses: a temporary file per transcode,
// removed when the session ends.
//
// It is a variable rather than a constant function so the composition root can
// substitute one — and so this package names the filesystem adapter in exactly
// one place.
var DefaultSpool NewSpool = func() (Spool, error) {
	return filesystem.TempSpool("mosaic-transcode-*.mp4")
}
