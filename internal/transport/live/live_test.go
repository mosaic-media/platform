// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package live

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/coder/websocket"
)

// TestConcurrentRouteAccessIsRaceFree guards the data race on session.current:
// the read loop writes the open route (setCurrent, on a navigate) while the
// input debounce timer, firing on its own goroutine, reads it (currentRoute, via
// pushContent) to return to the open screen when the search field is cleared.
// Under -race, this concurrent write/read of the route — a struct holding a map
// — trips the detector unless routeMu guards it.
func TestConcurrentRouteAccessIsRaceFree(t *testing.T) {
	s := &session{}
	const iterations = 2000
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			// A navigate replaces the route with a fresh params map each time.
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

// safeName guards a client-supplied mutation name that is interpolated into a
// GraphQL query, so it must reject anything that is not a plain identifier.
func TestSafeName(t *testing.T) {
	valid := []string{"importContent", "a", "a1", "snake_case", "camelCase9"}
	for _, s := range valid {
		if !safeName(s) {
			t.Errorf("safeName(%q) = false, want true", s)
		}
	}
	invalid := []string{"", "1leading", "has space", "paren()", "brace{}", "dash-name", "dot.name", "quote\"", "semi;drop"}
	for _, s := range invalid {
		if safeName(s) {
			t.Errorf("safeName(%q) = true, want false (injection risk)", s)
		}
	}
}

// TestShutdownClosesGoingAway proves the graceful-shutdown path (ADR 0032): a
// tracked session is closed with StatusGoingAway (1001) so the client treats it
// as a reconnect rather than an error. It exercises track → Shutdown → goAway
// against a real socket, without standing up a full app.Service.
func TestShutdownClosesGoingAway(t *testing.T) {
	sv := &Server{sessions: make(map[*session]struct{})}
	tracked := make(chan struct{})
	release := make(chan struct{})

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			t.Errorf("accept: %v", err)
			return
		}
		s := &session{c: c}
		if !sv.track(s) {
			t.Error("track returned false on a fresh server")
			return
		}
		close(tracked)
		// Keep the connection alive (without reading — goAway's Close owns the
		// close handshake) until the client has observed the close frame, so the
		// handler returning cannot tear the socket down under the client's read.
		<-release
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	wsURL := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.Dial(ctx, wsURL, nil)
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	defer c.CloseNow()

	<-tracked

	// Read concurrently with Shutdown, as a real client does: the pending read
	// observes the close frame and echoes it, so the server's Close completes
	// promptly instead of waiting out its handshake timeout.
	readErr := make(chan error, 1)
	go func() {
		_, _, e := c.Read(ctx)
		readErr <- e
	}()

	sv.Shutdown()
	err = <-readErr
	close(release)
	if got := websocket.CloseStatus(err); got != websocket.StatusGoingAway {
		t.Fatalf("close status = %d, want %d (StatusGoingAway)", got, websocket.StatusGoingAway)
	}

	// After Shutdown, a new session must be refused so none begins mid-shutdown.
	if sv.track(&session{}) {
		t.Error("track returned true after Shutdown; a new session should be refused")
	}
}
