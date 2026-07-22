// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package session is the first-party client session transport (ADR 0041): a
// typed, two-lane Connect/gRPC surface over protobuf. Client intents travel as
// unary calls (Attach/Navigate/Invoke/SubmitInput); server push travels as one
// server-streaming Subscribe call. It supersedes the bespoke WebSocket of
// ADR 0032 as the client transport and folds ADR 0033's handover into stream
// resume. The same application services back both this and the GraphQL surface,
// so this is a transport, not a second application layer.
//
// Concurrency follows ADR 0041's outbound-mailbox discipline: unary handlers
// never touch the stream directly (gRPC Send is not goroutine-safe). They
// enqueue onto a per-session buffer under the session lock; the single sender
// goroutine — the live Subscribe stream — drains that buffer to Send. Session
// state (route, input) is guarded explicitly. This file holds the session store
// and the mailbox/resume machinery; session.go holds the Connect handler and
// rendering; dispatch.go routes an Invoke to the application services.
package session

import (
	"context"
	"log"
	"sync"
	"time"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
	sessionv1 "github.com/mosaic-media/sdui/gen/mosaic/session/v1"
)

// historyLimit caps the per-session outbound buffer. Every pushed ServerMessage
// is retained here so a reconnecting client can resume from its last seq
// (ADR 0041). A live session's message rate is low, so this bounds memory while
// comfortably covering a reconnect window; a client whose cursor falls before
// the retained window is rebuilt from scratch rather than replayed.
const historyLimit = 256

// defaultSessionTTL is how long a session with no active Subscribe stream is
// kept before the reaper discards it. Its live state is disposable — a
// reconnecting client re-declares its route and the Platform rebuilds (ADR 0041,
// ADR 0032's resume principle), so discarding an idle session costs only a
// rebuild on the next connect.
const defaultSessionTTL = 5 * time.Minute

// route is the screen a session currently shows. A navigate replaces it
// wholesale (screen and its params map together), so a snapshot's map is never
// mutated after it is read.
type route struct {
	screen string
	params map[string]any
}

// liveSession is one client session, keyed by its opaque session ref (ADR 0017).
// It owns the outbound mailbox (history + seq), the current route and the
// input-coalescing state. Its zero value is not usable; build it with
// newLiveSession.
type liveSession struct {
	ref    string
	caller v1.Caller

	// routeMu guards current. Concurrent unary handlers write it (navigate,
	// attach) and the input-debounce timer reads it (to return to the open
	// screen when the search field clears).
	routeMu sync.Mutex
	current route

	// input-debounce state (ADR 0041's server-side coalescing, moved from the
	// ordered read loop of ADR 0032 into session state).
	inputMu    sync.Mutex
	inputTimer *time.Timer
	pendingIn  string

	// mu guards the outbound mailbox and lifecycle fields below; cond signals
	// the sender goroutine that new history is available (or that it should
	// exit). history is append-only per session, ordered by seq, trimmed to
	// historyLimit from the front.
	mu       sync.Mutex
	cond     *sync.Cond
	seq      uint64
	history  []*sessionv1.ServerMessage
	streams  int // active Subscribe streams (0 or 1 in normal use)
	closed   bool
	epoch    uint64 // bumped when a new Subscribe supersedes the prior one
	lastSeen time.Time
}

func newLiveSession(ref string, now time.Time) *liveSession {
	s := &liveSession{ref: ref, caller: v1.CallerFromSession(ref), lastSeen: now}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// enqueue assigns the next seq to msg, appends it to the outbound history and
// wakes the sender. It is the only way a message reaches the wire; a unary
// handler calls it and returns, and the Subscribe goroutine drains it. Returns
// the assigned seq (0 if the session is closed and the message is dropped).
func (s *liveSession) enqueue(msg *sessionv1.ServerMessage) uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return 0
	}
	s.seq++
	msg.Seq = s.seq
	s.history = append(s.history, msg)
	if len(s.history) > historyLimit {
		s.history = s.history[len(s.history)-historyLimit:]
	}
	s.cond.Broadcast()
	return s.seq
}

// resumePlan decides, for a connecting stream presenting cursor, whether the
// server can replay from the retained history or must rebuild. rebuild is true
// when the client is fresh (cursor 0), ahead of our state (we lost it — a
// restart or reap), or behind the retained window (its next message was
// evicted). from is the seq the sender starts after.
func (s *liveSession) resumePlan(cursor uint64) (from uint64, rebuild bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	switch {
	case cursor == 0:
		return s.seq, true
	case cursor > s.seq:
		return s.seq, true
	case len(s.history) > 0 && s.history[0].Seq > cursor+1:
		return s.seq, true
	default:
		return cursor, false
	}
}

// nextLocked returns the first buffered message with seq greater than cursor, or
// nil if the sender has drained everything available. Caller holds s.mu.
func (s *liveSession) nextLocked(cursor uint64) *sessionv1.ServerMessage {
	for _, m := range s.history {
		if m.Seq > cursor {
			return m
		}
	}
	return nil
}

// keepaliveInterval is how often an idle push lane sends a no-op.
//
// A Subscribe stream only carries traffic when the server has something to say,
// so a user reading a page sends nothing for minutes — and an idle HTTP
// connection is exactly what proxies, load balancers and container port
// forwarders reap. The client then correctly reports "Reconnecting" for a stream
// nothing was wrong with, and a reader who has touched nothing sees the
// connection drop repeatedly.
//
// Well inside the 60s that intermediaries commonly use, and cheap: an empty
// ServerMessage carries no body, so a client ignores it by the same default
// branch that ignores a message type it does not know.
const keepaliveInterval = 20 * time.Second

// serve is the single sender goroutine for a Subscribe stream. It supersedes any
// prior stream for this session (so a reconnect wins), runs onConnect for a
// fresh/rebuild connect, then drains the mailbox to send, replaying from the
// resume cursor and blocking on cond when caught up. It returns when the context
// ends, the session closes, a newer stream supersedes it, or send fails.
func (s *liveSession) serve(ctx context.Context, cursor uint64, onConnect func(), send func(*sessionv1.ServerMessage) error) error {
	from, rebuild := s.resumePlan(cursor)

	s.mu.Lock()
	s.epoch++
	myEpoch := s.epoch
	s.streams++
	// Wake any prior sender parked in cond.Wait so it observes the epoch change
	// and exits — a reconnect promptly retires the stream it replaces.
	s.cond.Broadcast()
	s.mu.Unlock()

	started := time.Now()
	log.Printf("session %s: stream open (resume=%d rebuild=%v epoch=%d)", s.ref, cursor, rebuild, myEpoch)
	defer func() {
		s.mu.Lock()
		s.streams--
		superseded := s.epoch != myEpoch
		closed := s.closed
		s.mu.Unlock()
		// Why a stream ended is the question that cannot be answered after the
		// fact without recording it, and every one of these looks identical to a
		// user: the page says "Reconnecting".
		log.Printf("session %s: stream closed after %s (%s)", s.ref, time.Since(started).Round(time.Millisecond),
			streamEndReason(ctx, superseded, closed))
	}()

	// Wake the sender when the request context ends, so a client that vanishes
	// unblocks the cond.Wait below rather than parking a goroutine forever.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		select {
		case <-ctx.Done():
		case <-stop:
		}
		s.mu.Lock()
		s.cond.Broadcast()
		s.mu.Unlock()
	}()

	// A fresh or rebuilt connect gets the shell and current content pushed
	// before draining; these enqueue with seqs after `from`, so the loop sends
	// them next.
	if rebuild {
		onConnect()
	}

	// A ticker broadcasts on cond so the parked sender wakes on a schedule and
	// can emit a keepalive. It has to go through cond rather than sending
	// directly, because send is only safe from this one goroutine.
	ka := time.NewTicker(keepaliveInterval)
	defer ka.Stop()
	go func() {
		for {
			select {
			case <-ka.C:
				s.mu.Lock()
				s.cond.Broadcast()
				s.mu.Unlock()
			case <-stop:
				return
			}
		}
	}()

	s.mu.Lock()
	defer s.mu.Unlock()
	lastSend := time.Now()
	for {
		if ctx.Err() != nil {
			return nil
		}
		if s.closed || s.epoch != myEpoch {
			return nil
		}
		msg := s.nextLocked(from)
		if msg == nil {
			// Caught up. Emit a keepalive if the lane has been quiet long
			// enough, then park again.
			if time.Since(lastSend) >= keepaliveInterval {
				s.mu.Unlock()
				err := send(&sessionv1.ServerMessage{})
				s.mu.Lock()
				if err != nil {
					return err
				}
				lastSend = time.Now()
				continue
			}
			s.cond.Wait()
			continue
		}
		s.mu.Unlock()
		err := send(msg)
		s.mu.Lock()
		if err != nil {
			return err
		}
		lastSend = time.Now()
		from = msg.Seq
	}
}

// streamEndReason names why a sender returned, for the close log.
func streamEndReason(ctx context.Context, superseded, closed bool) string {
	switch {
	case superseded:
		return "superseded by a reconnect"
	case closed:
		return "session closed"
	case ctx.Err() != nil:
		return "client disconnected: " + ctx.Err().Error()
	default:
		return "send failed"
	}
}

// stopInput cancels any pending debounced render, so a timer does not fire
// against a discarded session.
func (s *liveSession) stopInput() {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.inputTimer != nil {
		s.inputTimer.Stop()
		s.inputTimer = nil
	}
}

func (s *liveSession) setCurrent(r route) {
	s.routeMu.Lock()
	s.current = r
	s.routeMu.Unlock()
}

func (s *liveSession) currentRoute() route {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	return s.current
}

// Manager is the session store. It finds-or-creates a liveSession per opaque
// ref, retires idle ones, and closes them all on shutdown. Construct with
// NewManager.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*liveSession
	closed   bool
	clock    func() time.Time
}

// NewManager builds an empty session store.
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*liveSession), clock: time.Now}
}

// session finds or creates the live state for a ref and marks it seen. Every
// intent and every Subscribe funnels through here, so the ref keys one shared
// session whether the client subscribed first or fired an intent first.
func (m *Manager) session(ref string) *liveSession {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[ref]
	if s == nil {
		s = newLiveSession(ref, m.clock())
		if m.closed {
			s.closed = true
		}
		m.sessions[ref] = s
	}
	s.mu.Lock()
	s.lastSeen = m.clock()
	s.mu.Unlock()
	return s
}

// reap discards sessions with no active stream that have been idle past ttl,
// returning how many were removed. It is pure over the injected now, so a test
// drives it without waiting on wall-clock.
func (m *Manager) reap(now time.Time, ttl time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	removed := 0
	for ref, s := range m.sessions {
		s.mu.Lock()
		idle := s.streams == 0 && now.Sub(s.lastSeen) > ttl
		if idle {
			log.Printf("session %s: reaped after %s idle with no stream", ref, now.Sub(s.lastSeen).Round(time.Second))
		}
		s.mu.Unlock()
		if idle {
			s.stopInput()
			delete(m.sessions, ref)
			removed++
		}
	}
	return removed
}

// StartReaper runs reap on a ticker until ctx ends. Wire it to the serve
// context so idle sessions do not accumulate across a long-running process.
func (m *Manager) StartReaper(ctx context.Context) {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-t.C:
				m.reap(now, defaultSessionTTL)
			}
		}
	}()
}

// Shutdown closes every session: the sender goroutines return and their streams
// end, which a client treats as a reconnect (ADR 0041 stream resume), the way
// ADR 0032's "going away" close did for the WebSocket. Wire it through
// http.Server.RegisterOnShutdown so it fires as graceful shutdown begins.
func (m *Manager) Shutdown() {
	m.mu.Lock()
	m.closed = true
	all := make([]*liveSession, 0, len(m.sessions))
	for _, s := range m.sessions {
		all = append(all, s)
	}
	m.mu.Unlock()

	for _, s := range all {
		s.mu.Lock()
		s.closed = true
		s.cond.Broadcast()
		s.mu.Unlock()
		s.stopInput()
	}
}
