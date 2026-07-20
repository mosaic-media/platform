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
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
	"github.com/graphql-go/graphql"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/transport/screens"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

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

// Handler upgrades a request to the live session. It builds its own screen
// service over the application services (cheap — a wrapper), and holds the
// GraphQL schema so an invoke intent reuses the existing mutation resolvers.
func Handler(svc *app.Service, schema graphql.Schema, artwork func(string) string) http.Handler {
	screenSvc := screens.NewService(svc, artwork)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The dev client reaches this through the Vite proxy, so the Origin does
		// not match the Host; skip the check (the session is still authenticated
		// by its session id in the hello message).
		c, err := websocket.Accept(w, r, &websocket.AcceptOptions{InsecureSkipVerify: true})
		if err != nil {
			return
		}
		defer c.CloseNow()

		s := &session{c: c, screens: screenSvc, schema: schema}
		// A live session outlives a single request; run it against a background
		// context that ends when the socket closes, not when r's context does.
		if err := s.run(context.Background()); err != nil && !isClosed(err) {
			_ = c.Close(websocket.StatusInternalError, "session ended")
		}
	})
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
	current route
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

	// Push the app shell, then the initial content.
	if err := s.pushShell(ctx); err != nil {
		return err
	}
	s.current = route{screen: "search"}
	s.pushContent(ctx)

	for {
		var msg clientMsg
		if err := wsjson.Read(ctx, s.c, &msg); err != nil {
			return err
		}
		switch msg.Kind {
		case "navigate":
			s.current = route{screen: msg.Screen, params: msg.Params}
			s.pushContent(ctx)
		case "input":
			// Search-as-you-type: render the search screen for the current text.
			// This does not become the navigation route, so clearing the field
			// returns to whatever was open.
			s.pushRender(ctx, "search", map[string]any{"text": msg.Value})
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

// pushContent renders the current route into the content region.
func (s *session) pushContent(ctx context.Context) {
	s.pushRender(ctx, s.current.screen, s.current.params)
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
	_ = s.write(ctx, serverMsg{T: "toast", Message: "Added to library", Tone: "success"})
	s.pushContent(ctx)
}

func (s *session) write(ctx context.Context, msg serverMsg) error {
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
