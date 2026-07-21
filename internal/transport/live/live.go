// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package live is the SDUI live session (ADR 0032): one persistent WebSocket per
// client. The client streams intents (navigate, input, invoke); the Platform
// pushes UI updates (render a region, toast). It supersedes request/response
// screen fetching as the primary path — the same application services back both,
// so this is a transport, not a second application layer.
package live

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/transport/screens"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// inputDebounce is the server-side coalescing window for search-as-you-type
// (ADR 0032). Rapid input intents within this window collapse to a single
// render for the latest text, so a fast typist can't fan out a request per
// keystroke to the upstream addons (Cinemeta/Torrentio) and trip their rate
// limits. It is the backstop the client's own debounce sits in front of, and is
// deliberately a touch shorter than the client's ~220ms so it never adds
// perceptible lag when the client already coalesced.
const inputDebounce = 150 * time.Millisecond

// clientMsg is an intent the client streams up.
type clientMsg struct {
	Kind     string         `json:"kind"`
	Session  string         `json:"session,omitempty"`  // hello
	Screen   string         `json:"screen,omitempty"`   // navigate
	Params   map[string]any `json:"params,omitempty"`   // navigate
	Value    string         `json:"value,omitempty"`    // input (search text)
	Mutation string         `json:"mutation,omitempty"` // invoke
	Input    map[string]any `json:"input,omitempty"`    // invoke
}

// serverMsg is a UI update the Platform pushes down.
type serverMsg struct {
	T       string `json:"t"` // shell | render | toast
	Region  string `json:"region,omitempty"`
	Node    any    `json:"node,omitempty"`
	Message string `json:"message,omitempty"`
	Tone    string `json:"tone,omitempty"`
}

// Server is the live-session transport surface. It owns the screen service and
// tracks every open session so a graceful shutdown can close them all with a
// "going away" status — which the client treats as a reconnect, not a fault
// (ADR 0032). Construct it once and mount Handler() on the API mux.
type Server struct {
	screens *screens.Service
	schema  graphql.Schema

	mu       sync.Mutex
	sessions map[*session]struct{}
	closed   bool
}

// NewServer wires the live transport over the application services. It builds
// its own screen service (cheap — a wrapper), and holds the GraphQL schema so an
// invoke intent reuses the existing mutation resolvers.
func NewServer(svc *app.Service, schema graphql.Schema, artwork func(string) string) *Server {
	return &Server{
		screens:  screens.NewService(svc, artwork),
		schema:   schema,
		sessions: make(map[*session]struct{}),
	}
}

// Handler upgrades a request to the live session.
func (sv *Server) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The dev client reaches this through the Vite proxy, so the Origin does
		// not match the Host; skip the check (the session is still authenticated
		// by its session id in the hello message).
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()

		s := &session{c: c, screens: sv.screens, schema: sv.schema}
		if !sv.track(s) {
			// Shutdown is already in progress; do not start a new session.
			_ = c.Close(websocket.StatusGoingAway, "server shutting down")
			return
		}
		defer sv.untrack(s)

		// A live session outlives a single request; run it against a background
		// context that ends when the socket closes, not when r's context does.
		if err := s.run(context.Background()); err != nil && !isClosed(err) {
			_ = c.Close(websocket.StatusInternalError, "session ended")
		}
	})
}

// track registers an open session, returning false if the server is already
// shutting down (so no new session begins mid-shutdown).
func (sv *Server) track(s *session) bool {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	if sv.closed {
		return false
	}
	sv.sessions[s] = struct{}{}
	return true
}

func (sv *Server) untrack(s *session) {
	sv.mu.Lock()
	defer sv.mu.Unlock()
	delete(sv.sessions, s)
}

// Shutdown closes every open session with a "going away" status (1001). The
// client reads that as an intentional server departure and reconnects with
// backoff, rather than surfacing an error — which is what lets a Supervisor
// rolling upgrade drop its clients and have them self-heal (ADR 0032). Wire it
// through http.Server.RegisterOnShutdown so it fires as graceful shutdown
// begins; hijacked WebSocket connections are not otherwise drained by
// http.Server.Shutdown.
func (sv *Server) Shutdown() {
	sv.mu.Lock()
	sv.closed = true
	open := make([]*session, 0, len(sv.sessions))
	for s := range sv.sessions {
		open = append(open, s)
	}
	sv.mu.Unlock()

	for _, s := range open {
		s.goAway()
	}
}

type route struct {
	screen string
	params map[string]any
}

type session struct {
	c       *websocket.Conn
	screens *screens.Service
	schema  graphql.Schema
	caller  v1.Caller
	sid     string

	// routeMu guards current. The read loop writes it (on navigate); the input
	// debounce timer, firing on its own goroutine, reads it to return to the open
	// screen when the search field is cleared. Without the lock those two race on
	// a struct holding a map.
	routeMu sync.Mutex
	current route

	// writeMu serializes all writes to the socket. The read loop and the input
	// debounce timer (which fires on its own goroutine) both write, and the
	// underlying connection allows only one writer at a time.
	writeMu sync.Mutex

	// input debounce state (ADR 0032's server-side coalescing).
	inputMu    sync.Mutex
	inputTimer *time.Timer
	pendingIn  string
}

func (s *session) run(ctx context.Context) error {
	// The first message must be a hello carrying the session id.
	var hello clientMsg
	if err := wsjson.Read(ctx, s.c, &hello); err != nil {
		return err
	}
	if hello.Kind != "hello" || hello.Session == "" {
		return errors.New("live: first message must be hello with a session")
	}
	s.sid = hello.Session
	s.caller = v1.CallerFromSession(hello.Session)
	defer s.stopInput()

	// Push the app shell, then the initial content.
	if err := s.pushShell(ctx); err != nil {
		return err
	}
	s.setCurrent(route{screen: "home"})
	s.pushContent(ctx)

	for {
		var msg clientMsg
		if err := wsjson.Read(ctx, s.c, &msg); err != nil {
			return err
		}
		switch msg.Kind {
		case "navigate":
			s.setCurrent(route{screen: msg.Screen, params: msg.Params})
			s.pushContent(ctx)
		case "input":
			// Search-as-you-type: coalesce a burst of keystrokes and render the
			// search screen for the latest text. This does not become the
			// navigation route, so clearing the field returns to whatever was
			// open. Coalescing is the ADR 0032 backstop that protects the
			// upstream addons from a request per keystroke.
			s.onInput(ctx, msg.Value)
		case "invoke":
			s.invoke(ctx, msg)
		default:
			// Unknown intents are ignored rather than fatal, so a newer client
			// does not break an older server.
		}
	}
}

func (s *session) pushShell(ctx context.Context) error {
	node, err := s.screens.Render(ctx, "shell", s.caller, nil)
	if err != nil {
		return err
	}
	return s.write(ctx, serverMsg{T: "shell", Node: node})
}

// setCurrent records the open route under routeMu.
func (s *session) setCurrent(r route) {
	s.routeMu.Lock()
	s.current = r
	s.routeMu.Unlock()
}

// currentRoute returns a snapshot of the open route under routeMu. A navigate
// replaces the route (and its params map) wholesale, so the returned map is
// never mutated after being read here.
func (s *session) currentRoute() route {
	s.routeMu.Lock()
	defer s.routeMu.Unlock()
	return s.current
}

// pushContent renders the current route into the content region.
func (s *session) pushContent(ctx context.Context) {
	r := s.currentRoute()
	s.pushRender(ctx, r.screen, r.params)
}

// pushRender renders a screen and replaces the content region with it.
func (s *session) pushRender(ctx context.Context, screen string, params map[string]any) {
	node, err := s.screens.Render(ctx, screen, s.caller, params)
	if err != nil {
		_ = s.write(ctx, serverMsg{T: "render", Region: "content", Node: errorNode(err.Error())})
		return
	}
	_ = s.write(ctx, serverMsg{T: "render", Region: "content", Node: node})
}

// invoke runs a mutation through the GraphQL schema (reusing its resolvers),
// pushes a toast, and re-renders the current content so the change shows.
func (s *session) invoke(ctx context.Context, msg clientMsg) {
	if !safeName(msg.Mutation) {
		_ = s.write(ctx, serverMsg{T: "toast", Message: "unknown action", Tone: "danger"})
		return
	}
	doc := "mutation Live($input: JSON, $sid: String){ " + msg.Mutation + "(callerSessionId: $sid, input: $input) }"
	res := graphql.Do(graphql.Params{
		Schema:         s.schema,
		RequestString:  doc,
		VariableValues: map[string]any{"input": msg.Input, "sid": s.sid},
		Context:        ctx,
	})
	if len(res.Errors) > 0 {
		_ = s.write(ctx, serverMsg{T: "toast", Message: res.Errors[0].Message, Tone: "danger"})
		return
	}
	_ = s.write(ctx, serverMsg{T: "toast", Message: invokeToast(msg.Mutation), Tone: "success"})
	s.pushContent(ctx)
}

// invokeToast is the confirmation shown when a mutation succeeds. It reflects the
// action rather than assuming a library import, so a settings change does not
// claim to have added something to the library.
func invokeToast(mutation string) string {
	switch mutation {
	case "importContent":
		return "Added to library"
	case "configureModule":
		return "Settings saved"
	default:
		return "Done"
	}
}

// onInput records the latest search text and (re)arms the debounce timer, so a
// burst of keystrokes collapses to one render for the final value. The timer
// fires on its own goroutine, hence the write serialization in write().
func (s *session) onInput(ctx context.Context, text string) {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	s.pendingIn = text
	if s.inputTimer != nil {
		s.inputTimer.Stop()
	}
	s.inputTimer = time.AfterFunc(inputDebounce, func() {
		s.inputMu.Lock()
		t := s.pendingIn
		s.inputMu.Unlock()
		if strings.TrimSpace(t) == "" {
			// Cleared the field: return the content region to the current screen
			// (home, a detail, …) rather than an empty search.
			s.pushContent(ctx)
			return
		}
		s.pushRender(ctx, "search", map[string]any{"text": t})
	})
}

// stopInput cancels any pending debounced render when the session ends, so the
// timer does not fire against a closed socket.
func (s *session) stopInput() {
	s.inputMu.Lock()
	defer s.inputMu.Unlock()
	if s.inputTimer != nil {
		s.inputTimer.Stop()
		s.inputTimer = nil
	}
}

// goAway closes the socket with a "going away" status so the client reconnects
// rather than treating the drop as an error (ADR 0032). It takes the write lock
// so it does not race an in-flight push.
func (s *session) goAway() {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	_ = s.c.Close(websocket.StatusGoingAway, "server shutting down")
}

func (s *session) write(ctx context.Context, msg serverMsg) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	wctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	return wsjson.Write(wctx, s.c, msg)
}

// safeName guards the mutation name interpolated into the query, so a client
// cannot inject GraphQL. It must be a plain identifier.
func safeName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		alpha := r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
		if !alpha && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func errorNode(message string) any {
	return map[string]any{
		"type":  "ErrorState",
		"props": map[string]any{"category": "Unavailable", "message": message},
	}
}

// isClosed reports whether err is a normal socket closure rather than a fault.
func isClosed(err error) bool {
	return errors.Is(err, context.Canceled) ||
		websocket.CloseStatus(err) != -1
}
