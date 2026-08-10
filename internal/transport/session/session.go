// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"connectrpc.com/connect"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/contracts/gen/mosaic/session/v1/sessionv1connect"
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/rpc"
	"github.com/mosaic-media/platform/internal/transport/screens"
	"github.com/mosaic-media/platform/internal/transport/vocabulary"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// inputDebounce is the server-side coalescing window for search-as-you-type
// (contracts#5, preserving platform#22's backstop). Rapid SubmitInput intents within
// this window collapse to a single render for the latest text, so a fast typist
// cannot fan out a request per keystroke to the upstream addons. It sits behind
// the client's own ~220ms debounce and is a touch shorter so it never adds
// perceptible lag when the client already coalesced.
const inputDebounce = 150 * time.Millisecond

// debounceRenderTimeout bounds the background render a debounce timer runs. The
// timer fires after the SubmitInput call has returned, so it cannot use that
// call's context; it renders against a fresh bounded context instead.
const debounceRenderTimeout = 15 * time.Second

// contentRegion is the named slot the current screen renders into (platform#21).
// A navigate/input replaces this region; the shell frame around it persists.
const contentRegion = "content"

// playerRegion is where a playback surface is pushed (web#4). It is a region
// of its own rather than a replacement for the content region: a player sits
// *over* the current context, and the screen underneath must still be there when
// it closes.
const playerRegion = "player"

// The two payloads a client is handed before it draws anything (contracts#4) — the
// component library and the design tokens — live in the vocabulary package,
// because the pre-session bootstrap serves the same two to a client that has no
// session to be pushed to (platform#57). These name the Events this lane carries
// them on.

// definitionsEvent names the Event that carries the definition library. A client
// registers its payload (a JSON array of ComponentDefinition) before rendering.
const definitionsEvent = "sdui.definitions"

// tokensEvent names the Event that carries the token set. A client applies the
// payload over its bootstrap skin before rendering, so what it draws with is
// what the server said, not what it shipped with.
const tokensEvent = "sdui.tokens"

// Handler implements the SessionService (contracts#5). It owns the session store,
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
// artwork rewriter (platform#20), the same inputs the screen emit-side takes.
func NewHandler(svc *app.Service, artwork func(string) string, tickets TicketMinter, prober *playback.Prober) *Handler {
	h := &Handler{
		mgr:     NewManager(),
		screens: screens.NewService(svc, artwork),
		svc:     svc,
		tickets: tickets,
		prober:  prober,
	}
	// A reaped or shut-down session flushes whatever position it was holding
	// (platform#26). Coalescing is what makes this necessary: without it, closing
	// the lid on a paused film would lose the last few seconds of where you got
	// to, which is the only part of a position anyone notices.
	h.mgr.OnRetire(h.flushProgress)
	return h
}

// TicketMinter seals a resolved upstream location into the opaque ticket a
// client fetches bytes through (platform#25). It is an interface here so the
// session transport does not depend on the playback transport: both are
// transports, and one importing the other would be the wrong direction.
type TicketMinter interface {
	Mint(url string, headers map[string]string, session string, plan playback.Plan, opts ...playback.TicketOption) (string, error)
}

// Manager exposes the session store for lifecycle wiring (reaper, shutdown).
func (h *Handler) Manager() *Manager { return h.mgr }

// bind resolves the credential a call presented to its live session (platform#58).
//
// This is where the transport authenticates. Every call on this service carries
// an access token, and until it is resolved there is nothing to key live state
// by: the token rotates every few minutes and the session does not, so the
// mailbox, the replay cursor and the current route belong to the session id
// rather than to whatever the client happened to be holding when it connected.
//
// A refusal here is Unauthenticated, which is the whole reason a client can
// implement refresh at all: before this, an expired credential reached the
// screen builders and came back as an error *rendered into the content region*,
// so the client saw a successful call carrying a picture of a failure and had
// nothing to retry on. That was found by opening a browser twenty-five hours
// after signing in, and by nothing else.
func (h *Handler) bind(ctx context.Context, credential string) (*liveSession, error) {
	if credential == "" {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("session is required"))
	}
	caller := v1.CallerFromSession(credential)
	session, err := h.svc.SessionForCaller(ctx, caller)
	if err != nil {
		return nil, rpc.Wrap(err)
	}
	s := h.mgr.session(string(session.ID))
	// The credential is recorded per call rather than at construction: a client
	// that refreshed is presenting a new one, and everything rendered for it
	// must authorise as the caller that actually asked.
	s.setCaller(caller)
	return s, nil
}

// Attach (re)binds a caller to a session and, if a screen is named, re-asserts
// it and re-renders the content — the reconnect re-declaration platform#22 did over
// the socket, now an explicit intent (contracts#5). With no screen it just ensures
// the session exists.
func (h *Handler) Attach(ctx context.Context, req *connect.Request[sessionv1.AttachRequest]) (*connect.Response[sessionv1.Ack], error) {
	r := req.Msg
	s, err := h.bind(ctx, r.GetSession())
	if err != nil {
		return nil, err
	}
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
	// What the client can *render*, recorded the same way and for the same
	// reason. The one line it writes is the answer to a question nothing could
	// previously ask: is this client behind the contract, and by what?
	if vp := r.GetVocabulary(); vp != nil {
		cv := vocabulary.From(vp)
		s.setVocabulary(cv)
		missingPrims, missingActions := cv.Missing()
		primitives, actions := cv.Counts()
		telemetry.From(ctx).Info("client vocabulary declared",
			telemetry.Identifier("session", s.ref),
			telemetry.String("version", cv.Version()),
			telemetry.Int("primitives", primitives),
			telemetry.Int("actions", actions),
			telemetry.String("missing_primitives", strings.Join(missingPrims, ",")),
			telemetry.String("missing_actions", strings.Join(missingActions, ",")))
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
	s, err := h.bind(ctx, r.GetSession())
	if err != nil {
		return nil, err
	}
	s.setCurrent(route{screen: r.GetScreen(), params: decodeParams(r.GetParams())})
	h.pushContent(ctx, s)
	return connect.NewResponse(&sessionv1.Ack{}), nil
}

// Invoke runs a named action (an SDUI Action, platform#19) and pushes its outcome.
// A malformed intent — an empty session or an unknown action — fails as a
// Connect error. A domain failure of a known action (an import that could not
// source, say) is a user-facing outcome, so it is surfaced as a danger toast on
// the push lane and the intent still Acks. Success pushes a confirmation toast
// and re-renders the current content so the change shows.
func (h *Handler) Invoke(ctx context.Context, req *connect.Request[sessionv1.InvokeRequest]) (*connect.Response[sessionv1.Ack], error) {
	r := req.Msg
	if !safeAction(r.GetAction()) {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("unknown action"))
	}
	s, err := h.bind(ctx, r.GetSession())
	if err != nil {
		return nil, err
	}
	outcomes, err := h.dispatch(ctx, s, r.GetAction(), r.GetInput())
	if err != nil {
		// A rejection that names fields goes to the fields (contracts#13). A toast
		// saying "that username is taken" is a sentence floating next to the
		// form rather than a mark on the box that is wrong, and on a form with
		// four inputs it does not say which.
		if msg, ok := fieldErrorsMsg(err); ok {
			s.enqueue(msg)
			return connect.NewResponse(&sessionv1.Ack{}), nil
		}
		s.enqueue(toastMsg(errorMessage(err), "danger"))
		return connect.NewResponse(&sessionv1.Ack{}), nil
	}
	// An action that produced its own surface pushes it — possibly a sequence
	// (a player, and the "Next episode" control beside it) — instead of a
	// confirmation toast, and must not re-render the content region: the screen
	// underneath the player has not changed and re-rendering it would tear the
	// player down.
	if len(outcomes) > 0 {
		for _, m := range outcomes {
			s.enqueue(m)
		}
		return connect.NewResponse(&sessionv1.Ack{}), nil
	}
	// A silent action gets neither. Confirming something the user did not do is
	// noise, and re-rendering underneath a player would tear it down mid-scene
	// — which is what a position report, arriving every few seconds while a film
	// plays, would otherwise do to itself.
	if silentAction(r.GetAction()) {
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
	s, err := h.bind(ctx, r.GetSession())
	if err != nil {
		return nil, err
	}
	h.onInput(s, r.GetValue())
	return connect.NewResponse(&sessionv1.Ack{}), nil
}

// Subscribe is the push lane: one long-lived server-stream per session. On a
// fresh or rebuilt connect it pushes the shell and initial content; on a resume
// it replays what the client missed from its cursor. It blocks until the client
// disconnects, the session closes, or a newer Subscribe supersedes it.
func (h *Handler) Subscribe(ctx context.Context, req *connect.Request[sessionv1.SubscribeRequest], stream *connect.ServerStream[sessionv1.ServerMessage]) error {
	r := req.Msg
	s, err := h.bind(ctx, r.GetSession())
	if err != nil {
		return err
	}
	onConnect := func() {
		if s.currentRoute().screen == "" {
			s.setCurrent(route{screen: "home"})
		}
		// The definition library must be registered before the shell or any
		// screen that uses it renders, so it goes first on the stream (contracts#2).
		s.enqueue(tokensMsg())
		s.enqueue(definitionsMsg(ctx, s))
		h.pushShell(ctx, s)
		h.pushContent(ctx, s)
	}
	return s.serve(ctx, r.GetResumeCursor(), onConnect, stream.Send)
}

// pushShell renders the app shell for the current route and enqueues it
// (platform#21).
func (h *Handler) pushShell(ctx context.Context, s *liveSession) {
	screen := s.currentRoute().screen
	s.setShellChrome(screen)
	node, err := h.screens.Render(ctx, "shell", s.currentCaller(), map[string]any{"screen": screen})
	if err != nil {
		s.enqueue(regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, errorNode(err.Error())))
		return
	}
	s.enqueue(shellMsg(ctx, s, node))
}

// pushContent renders the current route into the content region.
func (h *Handler) pushContent(ctx context.Context, s *liveSession) {
	r := s.currentRoute()
	h.pushRender(ctx, s, r.screen, r.params)
}

// pushRender renders a screen and replaces the content region with it, or an
// error node if the render fails (platform#19's error surface, unchanged).
func (h *Handler) pushRender(ctx context.Context, s *liveSession, screen string, params map[string]any) {
	// The frame the app wears is a property of the screen being shown, and the
	// two sides of Mosaic wear different ones. Re-push it when the side changes
	// — not on every navigation, because the tree is identical for every screen
	// on the same side and re-sending it would cost a payload per tap.
	if screens.ShellChromeFor(screen) != screens.ShellChromeFor(s.shellChrome()) {
		s.setShellChrome(screen)
		if node, err := h.screens.Render(ctx, "shell", s.currentCaller(), map[string]any{"screen": screen}); err == nil {
			s.enqueue(shellMsg(ctx, s, node))
		}
	}

	// What this client can decode, for the screens that describe a release
	// rather than play one (platform#28). Set here because this is where the live
	// session is in hand; every screen that does not want it ignores it.
	ctx = screens.WithClientCodecs(ctx, s.clientProfile().codecs())
	// What the render learns about its sources comes back beside the tree
	// (platform#30): whether it was built from stored answers, how old they are,
	// and which sources did not answer. Nothing outside the source-backed
	// screens writes into it.
	ctx, report := screens.WithReport(ctx)
	node, err := h.screens.Render(ctx, screen, s.currentCaller(), params)
	if err != nil {
		s.enqueue(regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, errorNode(err.Error())))
		return
	}
	s.enqueue(regionMsg(ctx, s, contentRegion, sessionv1.RegionUpdate_REPLACE, node))
	h.applyReport(ctx, s, report, route{screen: screen, params: params})
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

// --- ServerMessage constructors (encoding option (b), contracts#6: the typed
// mosaic.sdui.v1.UINode rides the envelope directly, no JSON step). ---

// Every node-bearing constructor takes the session and the ctx rather than just
// the node, and that is the enforcement: a new push path cannot forget to
// degrade, because it cannot construct the message without handing over the
// session whose declaration decides what the client can draw. A pass that is
// merely "remembered at each call site" is one that a later call site omits.

func shellMsg(ctx context.Context, s *liveSession, node sdui.Node) *sessionv1.ServerMessage {
	node = degradeFor(ctx, s, node, "shell")
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Shell{Shell: &sessionv1.ShellUpdate{UiNode: node}}}
}

func regionMsg(ctx context.Context, s *liveSession, region string, op sessionv1.RegionUpdate_Op, node sdui.Node) *sessionv1.ServerMessage {
	node = degradeFor(ctx, s, node, region)
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Region{Region: &sessionv1.RegionUpdate{Region: region, Op: op, UiNode: node}}}
}

// degradeFor removes what this client declared it cannot render and reports what
// went. For an undeclared client it is a no-op returning the same pointer.
func degradeFor(ctx context.Context, s *liveSession, node sdui.Node, where string) sdui.Node {
	out, d := vocabulary.Degrade(node, s.vocabulary())
	d.Report(ctx, s.ref, where)
	return out
}

// fieldErrorsMsg turns a Platform error carrying per-field rejections into the
// push that lands them on the fields, and reports whether there were any.
//
// The envelope is the same one the client's own validators fill, so a rejection
// from either side renders identically — which is the whole reason it is
// symmetric rather than a second, server-shaped error surface.
func fieldErrorsMsg(err error) (*sessionv1.ServerMessage, bool) {
	var perr *contracts.Error
	if !errors.As(err, &perr) || len(perr.Fields) == 0 {
		return nil, false
	}
	out := make([]*sessionv1.FieldError, 0, len(perr.Fields))
	for _, f := range perr.Fields {
		out = append(out, &sessionv1.FieldError{Field: f.Field, Message: f.Message})
	}
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_FieldErrors{
		FieldErrors: &sessionv1.FieldErrors{Errors: out, FormError: perr.Message},
	}}, true
}

func toastMsg(message, tone string) *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Toast{Toast: &sessionv1.Toast{Message: message, Tone: tone}}}
}

// definitionsMsg carries the SDUI component-definition library (contracts#2) as an
// Event whose JSON payload is an array of ComponentDefinition. It is pushed once
// on connect, before the shell, so the client can register the components any
// screen references.
// It is filtered against the client's declared vocabulary on the way out: a
// definition whose template needs a primitive this client did not declare is
// served with its fallback instead, when it has one.
func definitionsMsg(ctx context.Context, s *liveSession) *sessionv1.ServerMessage {
	payload := vocabulary.DefinitionsFor(ctx, s.vocabulary(), vocabulary.Library(), s.ref)
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Event{Event: &sessionv1.Event{Type: definitionsEvent, Payload: payload}}}
}

// tokensMsg carries the design tokens (contracts#4) as an Event whose JSON payload
// is the DTCG set. It is pushed BEFORE the definitions and the shell: a client
// that rendered a screen and then restyled it would flash the skin it shipped
// with before showing the one it was served.
func tokensMsg() *sessionv1.ServerMessage {
	return &sessionv1.ServerMessage{Body: &sessionv1.ServerMessage_Event{Event: &sessionv1.Event{Type: tokensEvent, Payload: vocabulary.Tokens()}}}
}

// silentAction reports whether an action's success should pass without a toast
// or a re-render.
//
// The default is the right one for an action a person pressed: say it worked,
// and show the change. It is wrong for one a *client* emits on a timer, and
// progress reporting is the first of those — a "Done" toast every fifteen
// seconds over a playing film, and a content re-render underneath it each time.
//
// A list rather than a flag on the action, because the property belongs to the
// action rather than to the caller: whoever sends reportProgress, it is still
// not something to confirm.
//
// signOut is here for a third reason again, and it is the sharpest one: the
// credential it just revoked is the credential the re-render would authorise
// with. Confirming and re-rendering would put an Unauthenticated error into the
// content region of a screen the client is about to replace with the doorway —
// a picture of a failure pushed over a success, which is precisely the shape
// platform#58's first defect had.
func silentAction(action string) bool {
	return action == "reportProgress" || action == "recordImpression" || action == "signOut"
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
	case "installExtension":
		return "Extension installed"
	case "uninstallExtension":
		return "Extension removed"
	case "setWatched":
		return "Updated"
	case "createAccount":
		return "Account created"
	case "setUserStatus":
		return "Account updated"
	case "grantPreset":
		return "Permissions granted"
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
// (platform#19's error surface).
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
