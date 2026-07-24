// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"sync"
	"testing"
	"time"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/ui"
)

// enqueueToast is a small helper: push a toast body and return its assigned seq.
func enqueueToast(s *liveSession, msg string) uint64 {
	return s.enqueue(toastMsg(msg, "info"))
}

// collector records what a sender goroutine sends, in order, and signals each
// arrival. It stands in for a Subscribe stream's Send.
type collector struct {
	mu   sync.Mutex
	msgs []*sessionv1.ServerMessage
	got  chan struct{}
}

func newCollector() *collector { return &collector{got: make(chan struct{}, 1024)} }

func (c *collector) send(m *sessionv1.ServerMessage) error {
	c.mu.Lock()
	c.msgs = append(c.msgs, m)
	c.mu.Unlock()
	c.got <- struct{}{}
	return nil
}

func (c *collector) seqs() []uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]uint64, len(c.msgs))
	for i, m := range c.msgs {
		out[i] = m.Seq
	}
	return out
}

// waitFor blocks until the collector has received n messages or the deadline
// passes.
func (c *collector) waitFor(t *testing.T, n int) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		c.mu.Lock()
		have := len(c.msgs)
		c.mu.Unlock()
		if have >= n {
			return
		}
		select {
		case <-c.got:
		case <-deadline:
			t.Fatalf("timed out waiting for %d messages, have %d", n, have)
		}
	}
}

// TestEnqueueAssignsMonotonicSeq proves the mailbox assigns a strictly
// increasing seq and retains messages in order — the invariant a resuming client
// depends on.
func TestEnqueueAssignsMonotonicSeq(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	var last uint64
	for i := 0; i < 5; i++ {
		seq := enqueueToast(s, "m")
		if seq != last+1 {
			t.Fatalf("enqueue %d: got seq %d, want %d", i, seq, last+1)
		}
		last = seq
	}
	if got := len(s.history); got != 5 {
		t.Fatalf("history len = %d, want 5", got)
	}
}

// TestHistoryTrimsToLimit proves the outbound buffer is bounded: past
// historyLimit, the oldest messages are evicted from the front while seq keeps
// climbing.
func TestHistoryTrimsToLimit(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	total := historyLimit + 50
	for i := 0; i < total; i++ {
		enqueueToast(s, "m")
	}
	if got := len(s.history); got != historyLimit {
		t.Fatalf("history len = %d, want %d", got, historyLimit)
	}
	if got := s.history[0].Seq; got != uint64(total-historyLimit+1) {
		t.Fatalf("oldest retained seq = %d, want %d", got, total-historyLimit+1)
	}
}

// TestResumePlan pins the replay-vs-rebuild decision table (ADR 0041 stream
// resume).
func TestResumePlan(t *testing.T) {
	build := func(seqs ...uint64) *liveSession {
		s := newLiveSession("sess-1", time.Now())
		for range seqs {
			enqueueToast(s, "m")
		}
		return s
	}

	// Fresh connect (cursor 0): rebuild, start after everything buffered.
	s := build(1, 2, 3)
	if from, rebuild := s.resumePlan(0); !rebuild || from != 3 {
		t.Fatalf("cursor 0: from=%d rebuild=%v, want from=3 rebuild=true", from, rebuild)
	}
	// Resume within the retained window: replay from the cursor.
	if from, rebuild := s.resumePlan(1); rebuild || from != 1 {
		t.Fatalf("cursor 1: from=%d rebuild=%v, want from=1 rebuild=false", from, rebuild)
	}
	// Cursor ahead of our state (state lost): rebuild.
	if from, rebuild := s.resumePlan(9); !rebuild || from != 3 {
		t.Fatalf("cursor 9: from=%d rebuild=%v, want from=3 rebuild=true", from, rebuild)
	}
	// Cursor behind the retained window (its next message was evicted): rebuild.
	evicted := newLiveSession("sess-2", time.Now())
	for i := 0; i < historyLimit+10; i++ {
		enqueueToast(evicted, "m")
	}
	if _, rebuild := evicted.resumePlan(1); !rebuild {
		t.Fatalf("cursor behind window: rebuild=false, want true")
	}
}

// TestServeFreshConnectRebuilds proves a fresh connect (cursor 0) runs onConnect
// and delivers only what it enqueues — not stale pre-connect history.
func TestServeFreshConnectRebuilds(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	enqueueToast(s, "stale") // seq 1: buffered before this connect

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	onConnect := func() {
		s.enqueue(shellMsg(ui.Component("").Build()))                            // seq 2
		s.enqueue(regionMsg(contentRegion, sessionv1.RegionUpdate_REPLACE, nil)) // seq 3
	}
	done := make(chan struct{})
	go func() { _ = s.serve(ctx, 0, onConnect, c.send); close(done) }()

	c.waitFor(t, 2)
	cancel()
	<-done

	got := c.seqs()
	if len(got) != 2 || got[0] != 2 || got[1] != 3 {
		t.Fatalf("fresh connect delivered seqs %v, want [2 3] (no stale seq 1)", got)
	}
}

// TestServeResumeReplaysThenTails proves a reconnect replays exactly the
// messages after the client's cursor, then keeps delivering live ones.
func TestServeResumeReplaysThenTails(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	enqueueToast(s, "m1") // 1
	enqueueToast(s, "m2") // 2
	enqueueToast(s, "m3") // 3

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	c := newCollector()
	noop := func() {}
	done := make(chan struct{})
	go func() { _ = s.serve(ctx, 1, noop, c.send); close(done) }() // client saw seq 1

	c.waitFor(t, 2)       // replays 2, 3
	enqueueToast(s, "m4") // 4: live
	c.waitFor(t, 3)
	cancel()
	<-done

	got := c.seqs()
	if len(got) != 3 || got[0] != 2 || got[1] != 3 || got[2] != 4 {
		t.Fatalf("resume delivered seqs %v, want [2 3 4]", got)
	}
}

// TestServeSupersededByReconnect proves a second Subscribe for a session retires
// the first: the prior sender returns promptly (ADR 0041 — a reconnect wins).
func TestServeSupersededByReconnect(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	first := newCollector()
	firstDone := make(chan struct{})
	go func() { _ = s.serve(ctx, 5, func() {}, first.send); close(firstDone) }() // no rebuild, parks

	// Give the first sender a moment to park in cond.Wait.
	time.Sleep(20 * time.Millisecond)

	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	second := newCollector()
	go func() { _ = s.serve(ctx2, 5, func() {}, second.send) }()

	select {
	case <-firstDone:
	case <-time.After(2 * time.Second):
		t.Fatal("first sender did not exit after being superseded")
	}
}

// TestShutdownEndsServe proves Manager.Shutdown ends an in-flight stream so the
// client reconnects (ADR 0041 stream resume, replacing ADR 0032's going-away
// close).
func TestShutdownEndsServe(t *testing.T) {
	m := NewManager()
	s := m.session("sess-1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCollector()
	done := make(chan struct{})
	go func() { _ = s.serve(ctx, 3, func() {}, c.send); close(done) }()

	time.Sleep(20 * time.Millisecond)
	m.Shutdown()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not end after Shutdown")
	}
}

// TestReapDiscardsIdleSessions proves the reaper discards only idle,
// stream-less sessions past the TTL, keyed on the injected clock.
func TestReapDiscardsIdleSessions(t *testing.T) {
	m := NewManager()
	base := time.Unix(1_700_000_000, 0)

	idle := m.session("idle")
	idle.mu.Lock()
	idle.lastSeen = base
	idle.mu.Unlock()

	recent := m.session("recent")
	recent.mu.Lock()
	recent.lastSeen = base.Add(2*defaultSessionTTL - time.Minute) // seen a minute ago
	recent.mu.Unlock()

	active := m.session("active")
	active.mu.Lock()
	active.lastSeen = base
	active.streams = 1 // a live stream: never reaped, however old
	active.mu.Unlock()

	removed := m.reap(context.Background(), base.Add(2*defaultSessionTTL), defaultSessionTTL)
	if removed != 1 {
		t.Fatalf("reap removed %d, want 1", removed)
	}
	if m.session("recent") == nil || m.session("active") == nil {
		t.Fatal("reap discarded a session it should have kept")
	}
}

// TestMailboxOrderingUnderConcurrentEnqueue is the -race guard for the
// outbound-mailbox discipline (ADR 0041): many goroutines enqueue while the
// single sender drains, and every message must reach the wire in strictly
// increasing seq order. The total stays under historyLimit so nothing is
// evicted mid-drain, making delivery complete as well as ordered (the
// overflow/eviction behaviour is pinned separately by TestHistoryTrimsToLimit).
func TestMailboxOrderingUnderConcurrentEnqueue(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	c := newCollector()
	done := make(chan struct{})
	// Begin enqueueing only once the subscriber is established (onConnect fires
	// after resumePlan captured from=0), so no message is treated as skippable
	// pre-connect history — the fresh-connect skip is pinned by
	// TestServeFreshConnectRebuilds.
	ready := make(chan struct{})
	go func() { _ = s.serve(ctx, 0, func() { close(ready) }, c.send); close(done) }()
	<-ready

	const writers, each = 8, 24 // 192 total < historyLimit
	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				enqueueToast(s, "m")
			}
		}()
	}
	wg.Wait()

	c.waitFor(t, writers*each)
	cancel()
	<-done

	got := c.seqs()
	if len(got) != writers*each {
		t.Fatalf("delivered %d messages, want %d", len(got), writers*each)
	}
	for i := 1; i < len(got); i++ {
		if got[i] <= got[i-1] {
			t.Fatalf("seqs not strictly increasing at %d: %d then %d", i, got[i-1], got[i])
		}
	}
}

// TestConcurrentRouteAccessIsRaceFree guards the route data race: concurrent
// unary handlers write the open route (setCurrent, on a navigate) while the
// input-debounce timer reads it (currentRoute) to return to the open screen when
// the search field clears.
func TestConcurrentRouteAccessIsRaceFree(t *testing.T) {
	s := newLiveSession("sess-1", time.Now())
	const iterations = 2000
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			s.setCurrent(route{screen: "detail", params: map[string]any{"nodeId": "n-1"}})
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			r := s.currentRoute()
			_, _ = r.screen, r.params
		}
	}()
	wg.Wait()
}

// TestIdleStreamSendsKeepalives is the fix for a reconnect nobody caused.
//
// A Subscribe stream only carries traffic when the server has something to say,
// so a user reading a page sends nothing for minutes — and an idle HTTP
// connection is what proxies, load balancers and container port forwarders reap.
// The client then reports "Reconnecting" for a stream that was working fine.
//
// The keepalive is an empty ServerMessage: no body, so a client ignores it
// through the same branch that ignores a message type it does not recognise.
func TestIdleStreamSendsKeepalives(t *testing.T) {
	s := newLiveSession("s-keepalive", time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan *sessionv1.ServerMessage, 8)
	send := func(m *sessionv1.ServerMessage) error {
		select {
		case got <- m:
		default:
		}
		return nil
	}

	done := make(chan struct{})
	go func() { _ = s.serve(ctx, 0, func() {}, send); close(done) }()

	// The stream is idle: nothing is ever enqueued. Without a keepalive it would
	// sit silent indefinitely, which is exactly the condition that gets it cut.
	deadline := time.After(keepaliveInterval * 3)
	for {
		select {
		case m := <-got:
			if m.GetBody() == nil {
				cancel()
				<-done
				return // a bodyless message is the keepalive
			}
		case <-deadline:
			cancel()
			<-done
			t.Fatalf("no keepalive within %s on an idle stream", keepaliveInterval*3)
		}
	}
}

// TestKeepaliveYieldsToRealMessages — a keepalive must never delay or displace
// actual content. It only fills silence.
func TestKeepaliveYieldsToRealMessages(t *testing.T) {
	s := newLiveSession("s-yield", time.Now())

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	got := make(chan *sessionv1.ServerMessage, 8)
	send := func(m *sessionv1.ServerMessage) error { got <- m; return nil }

	// Enqueue only once the stream is actually open. A fresh connect (cursor 0)
	// starts from the session's current seq and deliberately does not replay
	// what came before it — onConnect rebuilds instead — so a message enqueued
	// in the window before the stream opens is correctly skipped. An earlier
	// version of this test enqueued there and failed, which was the test being
	// wrong rather than the loop.
	ready := make(chan struct{})
	done := make(chan struct{})
	go func() { _ = s.serve(ctx, 0, func() { close(ready) }, send); close(done) }()
	<-ready

	s.enqueue(toastMsg("hello", "success"))

	select {
	case m := <-got:
		if m.GetBody() == nil {
			t.Fatal("a keepalive was sent ahead of a queued message")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("queued message never arrived")
	}
	cancel()
	<-done
}
