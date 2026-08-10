// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package playback

import (
	"context"
	"errors"
	"io"
	"strconv"
	"sync"
	"time"

	"github.com/mosaic-media/platform/internal/adapters/filesystem"
)

// The transcode session, keyed by segment (platform#64, platform#66).
//
// **Segment N is what ffmpeg produces when started at N × the segment length.**
// It is not the Nth segment of a continuous run, and that distinction is the
// whole design. A copied stream cuts only at the source's keyframes, so a run
// asked for six-second segments of a release with ten-second keyframes emits
// ten-second ones — but a *restart* at N × length re-anchors to the playlist's
// own arithmetic, so the error is one keyframe interval and never accumulates.
// Measured: a restart at 60 s produced a segment whose first timestamp was
// 60.000000, and one at 42 s produced 40.000000, which is the keyframe a copy
// can begin at and nowhere else.
//
// Everything below follows from that. A request for a segment no running
// transcode will reach is not a failure, it is a seek; and a run that falls far
// enough behind the request is replaced rather than waited for.

// segmentsAhead is how far past the last requested segment a transcode may run
// before it is paused.
//
// **This is what keeps a playback's disk footprint a window rather than a
// film.** Without it an audio-only remux — which proceeds at near-copy speed —
// races to the end of the release as fast as the upstream will serve it, so one
// click on Play writes tens of gigabytes. Ten segments is about a minute of
// lead, comfortably more than any player buffers ahead and far less than a
// release.
const segmentsAhead = 10

// segmentsBehind is how many played segments are kept before eviction. A player
// re-requests a segment it has just played more often than one might expect —
// a small backwards seek, a buffer flush — so evicting immediately would cost a
// restart for a scrub of a few seconds.
const segmentsBehind = 5

// segmentLookahead is how far past the frontier a request may reach and still
// count as read-ahead rather than a seek.
//
// A player asking for the next segment or two is buffering; one asking for a
// segment a minute away has been scrubbed. The threshold is not an optimisation:
// **a linear play needs it.** Where real segments run longer than nominal a
// continuous run produces fewer of them than the playlist names, so a viewer
// watching straight through eventually asks for a segment that run will never
// emit, and only a restart answers it.
const segmentLookahead = 3

// segmentPollInterval is how often the frontier is re-read from the directory.
// ffmpeg's temp_file flag makes a segment appear at its final name only when it
// is complete, so presence is the readiness signal and polling for it is honest.
const segmentPollInterval = 200 * time.Millisecond

// segmentWaitTimeout bounds how long a request waits for a segment its
// transcode has not reached. Past this the transcode is not keeping up and
// saying so beats holding the request open.
const segmentWaitTimeout = 60 * time.Second

// maxSessionsPerTicket bounds the transcodes one playback may have running, for
// the same reason the byte-addressed origin needed it: a scrubbing viewer must
// not accumulate processes. Two is a playing session and a seek in flight.
const maxSessionsPerTicket = 2

// sessionIdle is how long a session outlives its last reader before it is
// reaped. It covers the gap between a player finishing one segment and asking
// for the next, which is routine and must not cost a restart.
const sessionIdle = 30 * time.Second

// reapInterval is how often the reaper looks. Shorter than sessionIdle, so a
// session that goes idle is stopped within about one interval of becoming
// eligible rather than up to a whole one late.
const reapInterval = 10 * time.Second

// ErrSegmentUnavailable reports that a segment could not be produced.
var ErrSegmentUnavailable = errors.New("playback: the transcode did not produce this segment")

// segmentName is the file one segment is written to and served as. The index is
// the whole name because the index is the whole identity — it is a position in
// the release, not a label.
func segmentName(n int) string {
	if n == initSegment {
		return "init.mp4"
	}
	return strconv.Itoa(n) + ".m4s"
}

// SegmentSessions holds the running transcodes, keyed by ticket.
type SegmentSessions struct {
	mu  sync.Mutex
	by  map[string][]*segmentSession
	dir filesystem.NewSegmentDir
}

// NewSegmentSessions builds a registry over a directory factory. A nil factory
// yields one that reports itself unusable, so a Platform assembled without it
// serves the unseekable pipe rather than failing a playback.
func NewSegmentSessions(dir filesystem.NewSegmentDir) *SegmentSessions {
	return &SegmentSessions{by: map[string][]*segmentSession{}, dir: dir}
}

func (s *SegmentSessions) usable() bool { return s != nil && s.dir != nil }

// segmentSession is one running ffmpeg and the directory it fills.
type segmentSession struct {
	// origin is the segment index this transcode was started at. Segment
	// numbering is absolute across restarts because ffmpeg is given
	// -start_number, so a session's files are named for their position in the
	// release rather than their position in this run.
	origin int

	mu       sync.Mutex
	cond     *sync.Cond
	dir      filesystem.SegmentDir
	frontier int // highest segment index present, or origin-1 before any
	done     bool
	dead     bool

	stop     func()
	pause    func()
	resume   func()
	paused   bool
	readers  int
	lastWant int
	used     time.Time
	idleFrom time.Time
}

// Open returns a reader over segment n, starting or restarting the transcode as
// needed. The returned size is the segment's length in bytes, which the origin
// needs for its Content-Length.
func (s *SegmentSessions) Open(ctx context.Context, rx *Remuxer, key, url string, headers map[string]string, plan Plan, length time.Duration, n int) (io.ReadCloser, int64, error) {
	if !s.usable() {
		return nil, 0, ErrSegmentUnavailable
	}

	s.mu.Lock()
	live := s.by[key]
	for _, sess := range live {
		if sess.covers(n) {
			sess.want(n)
			s.mu.Unlock()
			return sess.read(ctx, n)
		}
	}

	// No running transcode reaches this position, so the request is a seek.
	sess, err := startSegmentSession(ctx, rx, s.dir, url, headers, plan, length, n)
	if err != nil {
		s.mu.Unlock()
		return nil, 0, err
	}
	live = append(live, sess)
	for len(live) > maxSessionsPerTicket {
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

	return sess.read(ctx, n)
}

// OpenInit returns the fMP4 initialisation segment.
//
// It is served from whichever transcode already has one rather than from a
// designated session, and that is safe because every run writes the same init:
// the streams are decided once, at mint time, and travel sealed in the ticket
// (platform#29), so two runs of the same ticket differ only in where they started.
// A player fetches it once, before anything else, so the common case is that a
// session is being started for segment zero at the same moment.
func (s *SegmentSessions) OpenInit(ctx context.Context, rx *Remuxer, key, url string, headers map[string]string, plan Plan, length time.Duration) (io.ReadCloser, int64, error) {
	if !s.usable() {
		return nil, 0, ErrSegmentUnavailable
	}

	s.mu.Lock()
	for _, sess := range s.by[key] {
		if !sess.isDead() {
			s.mu.Unlock()
			return sess.readInit(ctx)
		}
	}
	sess, err := startSegmentSession(ctx, rx, s.dir, url, headers, plan, length, 0)
	if err != nil {
		s.mu.Unlock()
		return nil, 0, err
	}
	s.by[key] = append(s.by[key], sess)
	s.mu.Unlock()
	return sess.readInit(ctx)
}

// Close stops every running transcode and removes every segment directory.
func (s *SegmentSessions) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, live := range s.by {
		for _, sess := range live {
			sess.kill()
		}
		delete(s.by, k)
	}
}

// Reap stops sessions whose last reader left more than sessionIdle ago.
func (s *SegmentSessions) Reap(now time.Time) int {
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

// StartReaper runs Reap until ctx ends. A transcode outlives the request that
// started it deliberately, so a ticker is the only thing that ends an abandoned
// one — the same arrangement, and the same reason, as the session manager's.
func (s *SegmentSessions) StartReaper(ctx context.Context) {
	if !s.usable() {
		return
	}
	go func() {
		t := time.NewTicker(reapInterval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				s.Reap(now)
			}
		}
	}()
}

// covers reports whether this session can serve segment n: at or after where it
// started, and not so far past what it has produced that the request is a seek.
func (t *segmentSession) covers(n int) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.dead || n < t.origin {
		return false
	}
	if t.done {
		// Finished, so what exists is all there will be and the frontier is
		// exact rather than advancing.
		return n <= t.frontier
	}
	return n <= t.frontier+segmentLookahead
}

func (t *segmentSession) want(n int) {
	t.mu.Lock()
	if n > t.lastWant {
		t.lastWant = n
	}
	t.used = time.Now()
	t.mu.Unlock()
}

func (t *segmentSession) lastUsed() time.Time {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.readers > 0 {
		return time.Now()
	}
	return t.used
}

func (t *segmentSession) idle(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.readers == 0 && !t.idleFrom.IsZero() && now.Sub(t.idleFrom) > sessionIdle
}

func (t *segmentSession) kill() {
	t.mu.Lock()
	if t.dead {
		t.mu.Unlock()
		return
	}
	t.dead = true
	dir, stop, resume, paused := t.dir, t.stop, t.resume, t.paused
	t.cond.Broadcast()
	t.mu.Unlock()

	// A paused process ignores a terminate, so it is resumed before it is
	// stopped. Without this a throttled transcode survives its own session and
	// holds its upstream connection open.
	if paused && resume != nil {
		resume()
	}
	if stop != nil {
		stop()
	}
	if dir != nil {
		_ = dir.Close()
	}
}

// startSegmentSession launches ffmpeg at the time this segment index maps to.
func startSegmentSession(ctx context.Context, rx *Remuxer, newDir filesystem.NewSegmentDir, url string, headers map[string]string, plan Plan, length time.Duration, n int) (*segmentSession, error) {
	dir, err := newDir()
	if err != nil {
		return nil, err
	}

	// Detached from the request, deliberately: a player finishing one segment
	// and asking for the next must not kill the process between them. The
	// reaper is what bounds that.
	stop, pause, resume, err := rx.Segment(context.WithoutCancel(ctx), url, headers, plan, dir.Path(), length, n)
	if err != nil {
		_ = dir.Close()
		return nil, err
	}

	t := &segmentSession{
		origin: n, dir: dir, frontier: n - 1,
		stop: stop, pause: pause, resume: resume,
		lastWant: n, used: time.Now(), idleFrom: time.Now(),
	}
	t.cond = sync.NewCond(&t.mu)
	go t.follow()
	return t, nil
}

// follow watches the directory, publishing the frontier as segments complete,
// throttling the encoder when it runs too far ahead and evicting what is well
// behind the viewer.
func (t *segmentSession) follow() {
	ticker := time.NewTicker(segmentPollInterval)
	defer ticker.Stop()

	stalled := 0
	for range ticker.C {
		t.mu.Lock()
		if t.dead {
			t.mu.Unlock()
			return
		}

		advanced := false
		for t.dir.Has(segmentName(t.frontier + 1)) {
			t.frontier++
			advanced = true
		}
		if advanced {
			stalled = 0
			t.cond.Broadcast()
		} else {
			stalled++
		}

		// The throttle. A transcode that has run far enough ahead of the viewer
		// is paused rather than allowed to fill the disk with a film nobody has
		// asked for yet.
		ahead := t.frontier - t.lastWant
		switch {
		case !t.paused && ahead >= segmentsAhead && t.pause != nil:
			t.pause()
			t.paused = true
		case t.paused && ahead < segmentsAhead/2 && t.resume != nil:
			t.resume()
			t.paused = false
		}

		// Eviction behind the playhead, which is the other half of keeping the
		// footprint a window. Only below the origin of this session's own
		// numbering, so a restart never deletes what another session is serving.
		for i := t.origin; i <= t.lastWant-segmentsBehind; i++ {
			_ = t.dir.Remove(segmentName(i))
		}

		// A paused encoder is not a stalled one, so the completion check only
		// applies while it is running.
		if !t.paused && stalled > int(segmentWaitTimeout/segmentPollInterval) {
			t.done = true
			t.cond.Broadcast()
			t.mu.Unlock()
			return
		}
		t.mu.Unlock()
	}
}

// read waits for segment n and opens it.
func (t *segmentSession) read(ctx context.Context, n int) (io.ReadCloser, int64, error) {
	t.mu.Lock()
	if t.dead {
		t.mu.Unlock()
		return nil, 0, ErrSegmentUnavailable
	}
	t.readers++
	t.idleFrom = time.Time{}
	t.mu.Unlock()

	release := func() {
		t.mu.Lock()
		t.readers--
		if t.readers <= 0 {
			t.readers = 0
			t.idleFrom = time.Now()
		}
		t.mu.Unlock()
	}

	if err := t.waitFor(ctx, n); err != nil {
		release()
		return nil, 0, err
	}
	body, size, err := t.dir.Open(segmentName(n))
	if err != nil {
		release()
		return nil, 0, err
	}
	return &segmentReader{ReadCloser: body, release: release}, size, nil
}

func (t *segmentSession) isDead() bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.dead
}

// readInit waits for the initialisation segment and opens it. It waits on the
// file rather than on the frontier, because ffmpeg writes the init before any
// segment — verified: three seconds into a realtime run the directory held
// init.mp4 and nothing else.
func (t *segmentSession) readInit(ctx context.Context) (io.ReadCloser, int64, error) {
	deadline := time.Now().Add(segmentWaitTimeout)
	for {
		if t.isDead() {
			return nil, 0, ErrSegmentUnavailable
		}
		if body, size, err := t.dir.Open(segmentName(initSegment)); err == nil {
			return body, size, nil
		}
		if ctx.Err() != nil {
			return nil, 0, ctx.Err()
		}
		if time.Now().After(deadline) {
			return nil, 0, ErrSegmentUnavailable
		}
		select {
		case <-ctx.Done():
			return nil, 0, ctx.Err()
		case <-time.After(segmentPollInterval):
		}
	}
}

// waitFor blocks until segment n exists, the transcode ends, or the caller goes
// away.
func (t *segmentSession) waitFor(ctx context.Context, n int) error {
	deadline := time.Now().Add(segmentWaitTimeout)

	// The condition variable cannot be woken by a context, so a watchdog
	// broadcasts periodically and the loop re-checks. Cheap, and it keeps the
	// waiting logic in one place.
	stopWatch := make(chan struct{})
	defer close(stopWatch)
	go func() {
		tick := time.NewTicker(segmentPollInterval)
		defer tick.Stop()
		for {
			select {
			case <-stopWatch:
				return
			case <-ctx.Done():
				t.mu.Lock()
				t.cond.Broadcast()
				t.mu.Unlock()
				return
			case <-tick.C:
				t.mu.Lock()
				t.cond.Broadcast()
				t.mu.Unlock()
			}
		}
	}()

	t.mu.Lock()
	defer t.mu.Unlock()
	for {
		switch {
		case t.dead:
			return ErrSegmentUnavailable
		case t.frontier >= n:
			return nil
		case ctx.Err() != nil:
			return ctx.Err()
		case t.done:
			return ErrSegmentUnavailable
		case time.Now().After(deadline):
			return ErrSegmentUnavailable
		}
		t.cond.Wait()
	}
}

// segmentReader releases the session's reader count when the response is done.
type segmentReader struct {
	io.ReadCloser
	release func()
	once    sync.Once
}

func (r *segmentReader) Close() error {
	r.once.Do(r.release)
	return r.ReadCloser.Close()
}

// DefaultSegmentDir is the directory a Platform uses: one temporary directory
// per transcode, removed when the session ends.
var DefaultSegmentDir filesystem.NewSegmentDir = func() (filesystem.SegmentDir, error) {
	return filesystem.TempSegmentDir("mosaic-segments-*")
}
