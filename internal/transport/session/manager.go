// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package session is the first-party client session transport (contracts#5): a
// typed, two-lane Connect/gRPC surface over protobuf. Client intents travel as
// unary calls (Attach/Navigate/Invoke/SubmitInput); server push travels as one
// server-streaming Subscribe call. It supersedes the bespoke WebSocket of
// platform#22 as the client transport and folds supervisor#4's handover into stream
// resume. The same application services back both this and the auth surface,
// so this is a transport, not a second application layer.
//
// Concurrency follows contracts#5's outbound-mailbox discipline: unary handlers
// never touch the stream directly (gRPC Send is not goroutine-safe). They
// enqueue onto a per-session buffer under the session lock; the single sender
// goroutine — the live Subscribe stream — drains that buffer to Send. Session
// state (route, input) is guarded explicitly. This file holds the session store
// and the mailbox/resume machinery; session.go holds the Connect handler and
// rendering; dispatch.go routes an Invoke to the application services.
package session

import (
	"context"
	"sync"
	"time"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/vocabulary"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// historyLimit caps the per-session outbound buffer. Every pushed ServerMessage
// is retained here so a reconnecting client can resume from its last seq
// (contracts#5). A live session's message rate is low, so this bounds memory while
// comfortably covering a reconnect window; a client whose cursor falls before
// the retained window is rebuilt from scratch rather than replayed.
const historyLimit = 256

// defaultSessionTTL is how long a session with no active Subscribe stream is
// kept before the reaper discards it. Its live state is disposable — a
// reconnecting client re-declares its route and the Platform rebuilds (contracts#5,
// platform#22's resume principle), so discarding an idle session costs only a
// rebuild on the next connect.
const defaultSessionTTL = 5 * time.Minute

// route is the screen a session currently shows. A navigate replaces it
// wholesale (screen and its params map together), so a snapshot's map is never
// mutated after it is read.
type route struct {
	screen string
	params map[string]any
}

// liveSession is one client session, keyed by its **session id** (platform#13,
// platform#58). It owns the outbound mailbox (history + seq), the current route and
// the input-coalescing state. Its zero value is not usable; build it with
// newLiveSession.
//
// It used to be keyed by the value the client presents, which was the session
// id until the credential became a bearer pair. An access token rotates every
// few minutes, so keying by it would orphan this whole structure on every
// refresh — cursor, route and mailbox — and the client would see a reconnect it
// did not ask for each time its credential turned over.
type liveSession struct {
	ref string

	// callerMu guards the credential the *current* caller presented. It changes
	// whenever the client refreshes, which is routine, so it is read under a
	// lock rather than fixed at construction.
	callerMu sync.Mutex
	caller   v1.Caller

	// routeMu guards current. Concurrent unary handlers write it (navigate,
	// attach) and the input-debounce timer reads it (to return to the open
	// screen when the search field clears).
	routeMu sync.Mutex
	current route

	// profileMu guards the declared client profile. Attach writes it and a
	// later Invoke reads it, on separate unary calls, so it needs its own lock
	// rather than riding the mailbox mutex the sender goroutine holds while it
	// is parked in cond.Wait.
	profileMu sync.Mutex
	profile   clientProfile
	// chromeScreen is the screen whose chrome the client was last sent.
	chromeScreen string
	// vocab is what the client declared it can *render*, guarded by the same
	// lock and for the same reason: Attach writes it and every later push reads
	// it. The two declarations arrive on the same call and are never read apart.
	vocab vocabulary.Client

	// input-debounce state (contracts#5's server-side coalescing, moved from the
	// ordered read loop of platform#22 into session state).
	inputMu    sync.Mutex
	inputTimer *time.Timer
	pendingIn  string

	// The standing notices this session is currently showing, and whether a
	// background revalidation is already running for it (platform#30).
	//
	// Per session rather than per process, because a source's health is global
	// and "has this viewer been told" is not: diffing a failure against a
	// process-wide counter would raise the notice on whichever session rendered
	// first and on none of the others. The revalidation flag is here for the
	// opposite reason — it bounds work, and one fan-out per session at a time is
	// what stops a viewer tapping between two stale screens from stacking one
	// per tap.
	noticeMu    sync.Mutex
	notices     map[string]bool
	revalidmark bool

	// progress-coalescing state (platform#26). Separate from the input lock
	// because the two coalesce independently: someone can be typing in the
	// search field while a player behind the overlay reports its position, and
	// one timer must not be able to block the other.
	progressMu      sync.Mutex
	progressTimer   *time.Timer
	pendingProgress progressEnvelope

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
	s := &liveSession{ref: ref, lastSeen: now}
	s.cond = sync.NewCond(&s.mu)
	return s
}

// markNotice records that this session is showing the notice named by id, and
// reports whether that is new. A repeat returns false, so a source that fails on
// every render raises one notice rather than a fifth copy of it.
func (s *liveSession) markNotice(id string) bool {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	if s.notices[id] {
		return false
	}
	if s.notices == nil {
		s.notices = make(map[string]bool)
	}
	s.notices[id] = true
	return true
}

// clearNotice forgets a notice this session was showing.
func (s *liveSession) clearNotice(id string) {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	delete(s.notices, id)
}

// noticesExcept lists the standing notices under prefix that are *not* justified
// by the given set of still-failing names — the ones to retract.
func (s *liveSession) noticesExcept(prefix string, keep []string) []string {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	var out []string
	for id := range s.notices {
		if len(id) <= len(prefix) || id[:len(prefix)] != prefix {
			continue
		}
		name := id[len(prefix):]
		found := false
		for _, k := range keep {
			if k == name {
				found = true
				break
			}
		}
		if !found {
			out = append(out, id)
		}
	}
	return out
}

// beginRevalidation claims the session's one revalidation slot, reporting
// whether it was free. endRevalidation releases it.
func (s *liveSession) beginRevalidation() bool {
	s.noticeMu.Lock()
	defer s.noticeMu.Unlock()
	if s.revalidmark {
		return false
	}
	s.revalidmark = true
	return true
}

func (s *liveSession) endRevalidation() {
	s.noticeMu.Lock()
	s.revalidmark = false
	s.noticeMu.Unlock()
}

// setCaller records the credential this connection is currently presenting, so
// everything the Platform renders for it authorises as the caller that asked.
func (s *liveSession) setCaller(caller v1.Caller) {
	s.callerMu.Lock()
	defer s.callerMu.Unlock()
	s.caller = caller
}

// currentCaller is the credential to forward into the application services.
func (s *liveSession) currentCaller() v1.Caller {
	s.callerMu.Lock()
	defer s.callerMu.Unlock()
	return s.caller
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

	// Bind the session identity once; every line below inherits it. The ref is
	// an opaque session reference (platform#13) — credential-adjacent, and never
	// safe to write verbatim — so it is digested rather than dropped, which
	// keeps two records about one session tied together without the log
	// holding the reference itself.
	lg := telemetry.From(ctx).For("session").With(telemetry.Identifier("session", s.ref))

	started := time.Now()
	lg.Info("stream open",
		telemetry.Int64("resume", int64(cursor)),
		telemetry.Bool("rebuild", rebuild),
		telemetry.Int64("epoch", int64(myEpoch)))
	defer func() {
		s.mu.Lock()
		s.streams--
		superseded := s.epoch != myEpoch
		closed := s.closed
		s.mu.Unlock()
		// Why a stream ended is the question that cannot be answered after the
		// fact without recording it, and every one of these looks identical to a
		// user: the page says "Reconnecting".
		lg.Info("stream closed",
			telemetry.Duration("elapsed", time.Since(started).Round(time.Millisecond)),
			telemetry.String("reason", streamEndReason(ctx, superseded, closed)))
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

// stopTimers cancels any pending debounced work, so a timer does not fire
// against a discarded session.
//
// The position is *not* discarded with it — the Handler flushes it first, in
// reap and in Shutdown. Discarding it here would make being reaped the one way
// to lose exactly the position that mattered most.
func (s *liveSession) stopTimers() {
	s.stopInput()
	s.cancelProgress()
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

// The chrome the client is currently wearing.
//
// Tracked on the session rather than derived from the route, because the
// question being asked is "does the frame the client already has still fit",
// and the route has moved by the time that is asked. It starts empty, so the
// first render after connect always settles it.
func (s *liveSession) shellChrome() string {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	return s.chromeScreen
}

func (s *liveSession) setShellChrome(screen string) {
	s.routeMu.Lock()
	s.chromeScreen = screen
	s.routeMu.Unlock()
}

// setProfile records what the client declared it can decode (web#4).
func (s *liveSession) setProfile(p clientProfile) {
	s.profileMu.Lock()
	s.profile = p
	s.profileMu.Unlock()
}

// clientProfile returns what the client declared, or the assumption the Platform
// made for everyone before the declaration existed.
//
// The fallback is deliberate rather than defensive. A session can exist before
// any Attach — an intent may arrive first, and the manager finds-or-creates on
// either — so "no profile yet" is a normal state and not an error to report.
func (s *liveSession) clientProfile() clientProfile {
	s.profileMu.Lock()
	defer s.profileMu.Unlock()
	if !s.profile.declared {
		return clientProfile{prefer: browserPreference(), class: legacyBrowserClass}
	}
	return s.profile
}

// setVocabulary records what the client declared it can render (platform#52).
func (s *liveSession) setVocabulary(v vocabulary.Client) {
	s.profileMu.Lock()
	s.vocab = v
	s.profileMu.Unlock()
}

// vocabulary returns the declaration, or the zero value — which is undeclared,
// and means "send everything", the behaviour every client had before the
// declaration existed. A session can exist before any Attach, so undeclared is a
// normal state rather than an error.
func (s *liveSession) vocabulary() vocabulary.Client {
	s.profileMu.Lock()
	defer s.profileMu.Unlock()
	return s.vocab
}

// Manager is the session store. It finds-or-creates a liveSession per opaque
// ref, retires idle ones, and closes them all on shutdown. Construct with
// NewManager.
type Manager struct {
	mu       sync.Mutex
	sessions map[string]*liveSession
	closed   bool
	clock    func() time.Time
	// onRetire runs once per session as it is discarded — reaped or shut down.
	//
	// It exists because coalescing creates a way to lose the one write that
	// matters (platform#26): a pending position is held for a few seconds, and
	// being reaped or shut down inside that window would drop the position a
	// viewer actually stopped at. The Manager cannot write it itself — it owns
	// sessions, not services — so the Handler supplies this.
	onRetire func(*liveSession)
}

// NewManager builds an empty session store.
func NewManager() *Manager {
	return &Manager{sessions: make(map[string]*liveSession), clock: time.Now}
}

// OnRetire registers what to do with a session before it is discarded.
func (m *Manager) OnRetire(fn func(*liveSession)) {
	m.mu.Lock()
	m.onRetire = fn
	m.mu.Unlock()
}

// retire runs the retirement hook and cancels the session's timers, in that
// order. The order is the point: flushing has to read the pending position
// before stopTimers clears it.
func (m *Manager) retire(s *liveSession) {
	if m.onRetire != nil {
		m.onRetire(s)
	}
	s.stopTimers()
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

// End closes one session and forgets it: its sender goroutine returns, its
// stream ends, and its live state is discarded.
//
// It is what a *revocation* needs and reaping does not provide. Signing out
// revokes the credential server-side, and without this the client carried on
// rendering: the push lane is a long-lived stream that makes no call to be
// refused, so nothing on either side noticed until the access token expired
// ten minutes later. A sign-out that takes ten minutes is not a sign-out, and
// on a shared device it is the whole feature failing.
//
// Ending the stream is also what makes it *one* path in the client: a dropped
// stream is a reconnect, the reconnect presents a revoked credential, and the
// client takes the same route it takes for any refused session — clear the
// pair, ask for the door. Nothing in the client has to know a sign-out
// happened.
func (m *Manager) End(ref string) {
	m.mu.Lock()
	s := m.sessions[ref]
	delete(m.sessions, ref)
	m.mu.Unlock()
	if s == nil {
		return
	}
	s.mu.Lock()
	s.closed = true
	s.cond.Broadcast()
	s.mu.Unlock()
	m.retire(s)
}

// reap discards sessions with no active stream that have been idle past ttl,
// returning how many were removed. It is pure over the injected now, so a test
// drives it without waiting on wall-clock.
func (m *Manager) reap(ctx context.Context, now time.Time, ttl time.Duration) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	lg := telemetry.From(ctx).For("session")
	removed := 0
	for ref, s := range m.sessions {
		s.mu.Lock()
		idle := s.streams == 0 && now.Sub(s.lastSeen) > ttl
		if idle {
			lg.Info("session reaped",
				telemetry.Identifier("session", ref),
				telemetry.Duration("idle", now.Sub(s.lastSeen).Round(time.Second)))
		}
		s.mu.Unlock()
		if idle {
			m.retire(s)
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
				m.reap(ctx, now, defaultSessionTTL)
			}
		}
	}()
}

// Shutdown closes every session: the sender goroutines return and their streams
// end, which a client treats as a reconnect (contracts#5 stream resume), the way
// platform#22's "going away" close did for the WebSocket. Wire it through
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
		m.retire(s)
	}
}
