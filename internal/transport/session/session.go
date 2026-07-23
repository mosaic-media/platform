// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/screens"
	sessionv1 "github.com/mosaic-media/sdui/gen/mosaic/session/v1"
	"github.com/mosaic-media/sdui/gen/mosaic/session/v1/sessionv1connect"
	sdui "github.com/mosaic-media/sdui/sdui"
	"github.com/mosaic-media/sdui/ui"
)

// inputDebounce is the server-side coalescing window for search-as-you-type
// (ADR 0041, preserving ADR 0032's backstop). Rapid SubmitInput intents within
// this window collapse to a single render for the latest text, so a fast typist
// cannot fan out a request per keystroke to the upstream addons. It sits behind
// the client's own ~220ms debounce and is a touch shorter so it never adds
// perceptible lag when the client already coalesced.
const inputDebounce = 150 * time.Millisecond

// debounceRenderTimeout bounds the background render a debounce timer runs. The
// timer fires after the SubmitInput call has returned, so it cannot use that
// call's context; it renders against a fresh bounded context instead.
const debounceRenderTimeout = 15 * time.Second

// contentRegion is the named slot the current screen renders into (ADR 0031).
// A navigate/input replaces this region; the shell frame around it persists.
const contentRegion = "content"

// playerRegion is where a playback surface is pushed (ADR 0047). It is a region
// of its own rather than a replacement for the content region: a player sits
// *over* the current context, and the screen underneath must still be there when
// it closes.
const playerRegion = "player"

// definitionsLibrary is the standard SDUI component-definition library as data
// (ADR 0024): every presentational component — Screen, AppShell, HeroBanner,
// PosterCard, Section … — expressed as a tree of primitives. The Platform pushes
// it to a client on connect (below), so the design system is server-owned: a
// client ships only the primitive vocabulary + the generic expander and renders
// whatever definitions the Platform sends. Redesigning a component is a Platform
// change, not a client release. Regenerate with the sdui-react repo's
// `scripts/dump-definitions.mjs` when a definition changes.
//
//go:embed definitions.json
var definitionsLibrary []byte

// definitionsEvent names the Event that carries the definition library. A client
// registers its payload (a JSON array of ComponentDefinition) before rendering.
const definitionsEvent = "sdui.definitions"

// Handler implements the SessionService (ADR 0041). It owns the session store,
// builds its own screen service (a thin projection wrapper over the application
// query services), and routes intents to the application command/query services.
// Construct once and mount its Connect handler on the API mux.
type Handler struct {
	mgr     *Manager
	screens *screens.Service
	svc     *app.Service
	tickets TicketMinter
	prober  *playback.Prober
}

// Compile-time proof the handler satisfies the generated service contract.
var _ sessionv1connect.SessionServiceHandler = (*Handler)(nil)

// NewHandler wires the session transport over the application services and the
// artwork rewriter (ADR 0030), the same inputs the screen emit-side takes.
func NewHandler(svc *app.Service, artwork func(string) string, tickets TicketMinter, prober *playback.Prober) *Handler {
	return &Handler{
		mgr:     NewManager(),
		screens: screens.NewService(svc, artwork),
		svc:     svc,
		tickets: tickets,
		prober:  prober,
	}
}

// TicketMinter seals a resolved upstream location into the opaque ticket a
// client fetches bytes through (ADR 0045). It is an interface here so the
// session transport does not depend on the playback transport: both are
// transports, and one importing the other would be the wrong direction.
type TicketMinter interface {
	Mint(url string, headers map[string]string, session string, plan playback.Plan) (string, error)
}

// Manager exposes the session store for lifecycle wiring (reaper, shutdown).
func (h *Handler) Manager() *Manager { return h.mgr }

// Attach (re)binds a caller to a session and, if a screen is named, re-asserts
// it and re-renders the content — the reconnect re-declaration ADR 0032 did over
// the socket, now an explicit intent (ADR 0041). With no screen it just ensures
// the session exists.
func (h *Handler) Attach(ctx context.Context, req *connect.Request[sessionv1.AttachRequest]) (*connect.Response[sessionv1.Ack], error) {
	r := req.Msg
	if r.GetSession() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session is required"))
	}
	s := h.mgr.session(r.GetSession())
	// The declaration rides Attach because it cannot change without a reconnect
	// and Attach is the one call every client makes on every connect. It is
	// recorded before anything renders, so the first screen a client sees is
	// already built against what that client can actually play.
	//
	// The field is optional, and an absent one is not an error: a client built
	// against an older contract still works, on the assumption that used to be
	// hard-coded for everybody.
	if p := r.GetProfile(); p != nil {
		cp := profileFrom(p)
		s.setProfile(cp)
		telemetry.From(ctx).For("session").Info("client profile declared",
			telemetry.Identifier("session", s.ref),
			telemetry.String("class", cp.class),
			telemetry.Int("video_codecs", len(cp.prefer.VideoCodecs)),
			telemetry.Int("audio_codecs", len(cp.prefer.AudioCodecs)),
			telemetry.Bool("hdr", cp.prefer.HDR),
			telemetry.Int("max_height", cp.prefer.MaxHeight))
	}
	if r.GetScreen() != "" {
		s.setCurrent(route{screen: r.GetScreen(), params: decodeParams(r.GetParams())})
		h.pushContent(ctx, s)
	}
	return connect.NewResponse(&sessionv1.Ack{}), nil
}

// Navigate opens a screen and renders it into the content region.
func (h *Handler) Navigate(ctx context.Context, req *connect.Request[sessionv1.NavigateRequest]) (*connect.Response[sessionv1.Ack], error) {
	r := req.Msg
	if r.GetSession() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session is required"))
	}
	s := h.mgr.session(r.GetSession())
	s.setCurrent(route{screen: r.GetScreen(), params: decodeParams(r.GetParams())})
	h.pushContent(ctx, s)
	return connect.NewResponse(&sessionv1.Ack{}), nil
}

// Invoke runs a named action (an SDUI Action, ADR 0029) and pushes its outcome.
// A malformed intent — an empty session or an unknown action — fails as a
// Connect error. A domain failure of a known action (an import that could not
// source, say) is a user-facing outcome, so it is surfaced as a danger toast on
// the push lane and the intent still Acks. Success pushes a confirmation toast
// and re-renders the current content so the change shows.
func (h *Handler) Invoke(ctx context.Context, req *connect.Request[sessionv1.InvokeRequest]) (*connect.Response[sessionv1.Ack], error) {
	r := req.Msg
	if r.GetSession() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session is required"))
	}
	if !safeAction(r.GetAction()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown action"))
	}
	s := h.mgr.session(r.GetSession())
	outcome, err := h.dispatch(ctx, s, r.GetAction(), r.GetInput())
	if err != nil {
		s.enqueue(toastMsg(errorMessage(err), "danger"))
		return connect.NewResponse(&sessionv1.Ack{}), nil
	}
	// An action that produced its own surface (a player) pushes that instead of
	// a confirmation toast, and must not re-render the content region — the
	// screen underneath the player has not changed and re-rendering it would
	// tear the player down.
	if outcome != nil {
		s.enqueue(outcome)
		return connect.NewResponse(&sessionv1.Ack{}), nil
	}
	s.enqueue(toastMsg(invokeToast(r.GetAction()), "success"))
	h.pushContent(ctx, s)
	return connect.NewResponse(&sessionv1.Ack{}), nil
}

// SubmitInput carries one search-as-you-type keystroke; the Platform coalesces a
// burst and renders the search screen for the latest text. This does not become
// the navigation route, so clearing the field returns to whatever was open.
func (h *Handler) SubmitInput(ctx context.Context, req *connect.Request[sessionv1.InputRequest]) (*connect.Response[sessionv1.Ack], error) {
	r := req.Msg
	if r.GetSession() == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session is required"))
	}
	s := h.mgr.session(r.GetSession())
	h.onInput(s, r.GetValue())
	return connect.NewResponse(&sessionv1.Ack{}), nil
}

// Subscribe is the push lane: one long-lived server-stream per session. On a
// fresh or rebuilt connect it pushes the shell and initial content; on a resume
// it replays what the client missed from its cursor. It blocks until the client
// disconnects, the session closes, or a newer Subscribe supersedes it.
func (h *Handler) Subscribe(ctx context.Context, req *connect.Request[sessionv1.SubscribeRequest], stream *connect.ServerStream[sessionv1.ServerMessage]) error {
	r := req.Msg
	if r.GetSession() == "" {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("session is required"))
	}
	s := h.mgr.session(r.GetSession())
	onConnect := func() {
		if s.currentRoute().screen == "" {
			s.setCurrent(route{screen: "home"})
		}
		// The definition library must be registered before the shell or any
		// screen that uses it renders, so it goes first on the stream (ADR 0024).
		s.enqueue(definitionsMsg())
		h.pushShell(ctx, s)
		h.pushContent(ctx, s)
	}
	return s.serve(ctx, r.GetResumeCursor(), onConnect, stream.Send)
}

// pushShell renders the app shell and enqueues it (ADR 0031).
func (h *Handler) pushShell(ctx context.Context, s *liveSession) {
	node, err := h.screens.Render(ctx, "shell", s.caller, nil)
	if err != nil {
		s.enqueue(regionMsg(contentRegion, sessionv1.RegionUpdate_REPLACE, errorNode(err.Error())))
		return
	}
	s.enqueue(shellMsg(node))
}

// pushContent renders the current route into the content region.
func (h *Handler) pushContent(ctx context.Context, s *liveSession) {
	r := s.currentRoute()
	h.pushRender(ctx, s, r.screen, r.params)
}

// pushRender renders a screen and replaces the content region with it, or an
// error node if the render fails (ADR 0029's error surface, unchanged).
func (h *Handler) pushRender(ctx context.Context, s *liveSession, screen string, params map[string]any) {
	node, err := h.screens.Render(ctx, screen, s.caller, params)
	if err != nil {
		s.enqueue(regionMsg(contentRegion, sessionv1.RegionUpdate_REPLACE, errorNode(err.Error())))
		return
	}
	s.enqueue(regionMsg(contentRegion, sessionv1.RegionUpdate_REPLACE, node))
}

// onInput records the latest search text and (re)arms the debounce timer, so a
// burst of keystrokes collapses to one render for the final value. The timer
// fires on its own goroutine and enqueues, which the session lock makes safe.
func (h *Handler) onInput(s *liveSession, text string) {
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

		ctx, cancel := context.WithTimeout(context.Background(), debounceRenderTimeout)
		defer cancel()
		if strings.TrimSpace(t) == "" {
			// Cleared the field: return the content region to the open screen
			// rather than an empty search.
			h.pushContent(ctx, s)
			return
		}
		h.pushRender(ctx, s, "search", map[string]any{"text": t})
	})
}

// --- ServerMessage constructors (encoding option (b), ADR 0044: the typed
// mosaic.sdui.v1.UINode rides the envelope directly, no JSON step). ---

func shellMsg(node sdui.Node) *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Shell{Shell: &sessionv1.ShellUpdate{UiNode: node}}}
}

func regionMsg(region string, op sessionv1.RegionUpdate_Op, node sdui.Node) *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Region{Region: &sessionv1.RegionUpdate{Region: region, Op: op, UiNode: node}}}
}

func toastMsg(message, tone string) *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Toast{Toast: &sessionv1.Toast{Message: message, Tone: tone}}}
}

// definitionsMsg carries the SDUI component-definition library (ADR 0024) as an
// Event whose JSON payload is an array of ComponentDefinition. It is pushed once
// on connect, before the shell, so the client can register the components any
// screen references.
func definitionsMsg() *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Event{Event: &sessionv1.Event{Type: definitionsEvent, Payload: definitionsLibrary}}}
}

// invokeToast is the confirmation shown when an action succeeds. It reflects the
// action rather than assuming a library import, so a settings change does not
// claim to have added something to the library.
func invokeToast(action string) string {
	switch action {
	case "importContent":
		return "Added to library"
	case "configureModule":
		return "Settings saved"
	default:
		return "Done"
	}
}

// decodeParams reads a screen's JSON param object into the map the screen
// builders take. Absent or empty params decode to nil, which the builders read
// as "no params".
func decodeParams(b []byte) map[string]any {
	if len(b) == 0 {
		return nil
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return nil
	}
	return m
}

// errorNode is the ErrorState UINode a failed render puts in the content region
// (ADR 0029's error surface).
func errorNode(message string) sdui.Node {
	return ui.Component("ErrorState",
		ui.Prop("category", "Unavailable"), ui.Prop("message", message)).Build()
}

// errorMessage extracts a user-facing message from a Platform error, falling
// back to the raw error text for anything uncategorised.
func errorMessage(err error) string {
	var cErr *contracts.Error
	if errors.As(err, &cErr) {
		return cErr.Message
	}
	return err.Error()
}

// safeAction guards the action name against an unknown or malformed value. It
// must be a plain identifier — the dispatch switch then maps it to a service.
func safeAction(s string) bool {
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
