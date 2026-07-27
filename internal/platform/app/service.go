// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/sessions"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Service hosts Platform application command and query handlers. It holds
// direct read access to SessionStore, UserStore and CredentialStore for
// authentication and query paths, and a UnitOfWork for the transactional
// write path — the same contracts, reached through the shape appropriate
// to each operation. It is the enforcement point for policy
// decisions: the policy.PolicyDecisionPoint only decides; Service is what
// actually refuses to mutate state on a deny.
type Service struct {
	uow              contracts.UnitOfWork
	sessionStore     contracts.SessionStore
	users            contracts.UserStore
	credentials      contracts.CredentialStore
	configStore      contracts.ConfigStore
	permissions      contracts.PermissionStore
	moduleSettings   contracts.ModuleSettingsStore
	userPreferences  contracts.UserPreferenceStore
	telemetryQueries contracts.TelemetryQueryStore
	tokens           contracts.TokenStore
	nodes            contracts.NodeStore
	parts            contracts.PartStore
	resolutions      contracts.PlaybackResolutionStore
	playbackStates   contracts.PlaybackStateStore
	clock            contracts.Clock
	ids              contracts.IDGenerator
	contentIDs       contracts.IDGenerator
	policy           policy.PolicyDecisionPoint
	events           contracts.EventPublisher
	passwordVerifier domain.PasswordVerifier
	capabilities     *CapabilityRegistry
	extensions       ExtensionManager
	sessionManager   *sessions.Manager
	configManager    *config.Manager

	// telemetryMaintenance creates and drops the partitions stored telemetry
	// lives in (ADR 0058). Optional: a Service built without one refuses
	// PurgeTelemetry with Unavailable, which is what a test service or a
	// deployment with no queryable sink should do rather than pretending a
	// sweep happened.
	telemetryMaintenance contracts.TelemetryMaintenanceStore
	// jobs is the background-work queue (ADR 0017's no-user case). Optional
	// for the same reason.
	jobs contracts.JobStore
	// libraryRules is the direct read handle for what the library should
	// contain (ADR 0104). Writes go through the UnitOfWork like every other
	// mutation. Optional: a Service built without one reports no rules and
	// refuses to write any, which is the honest answer for a test service —
	// and it is what makes the maintenance handler a no-op rather than a panic
	// in a build with no store.
	libraryRules contracts.LibraryRuleStore
	// nodeMetadata is the direct read handle for what a provider said about a
	// materialised title (ADR 0107). Optional: a Service built without one
	// renders a library detail from the node alone, which is what every detail
	// did before the store existed.
	nodeMetadata contracts.NodeMetadataStore
	// watchAvailability is the direct handle for the queryable projection of
	// where a work can be watched (roadmap M2.5). Optional, on the same terms as
	// nodeMetadata: a Service built without one stores no availability and
	// offers no streaming-service facet, which is what every build did before
	// the store existed.
	watchAvailability contracts.WatchAvailabilityStore
	// instance is what this install calls itself, held outside PostgreSQL
	// (ADR 0098). Optional: a Service built without one has no name to report
	// and records none when a server is claimed, which is what a test service
	// or a deployment with no writable path should do.
	instance contracts.InstanceIdentityStore

	// systemSession is the opaque reference SystemCaller hands out. Minted per
	// process in NewService — see system_principal.go for why it is random
	// rather than a well-known constant.
	systemSession string
}

// Deps are the collaborators a Service is built from. They are passed as a named
// struct rather than positionally because the list is long and several members
// share a type: IDs and ContentIDs are both contracts.IDGenerator, and swapping
// them would compile cleanly while silently crossing the platform and content id
// generators. Named fields make each dependency explicit at the call site and
// remove that transposition footgun. Field names mirror the composition root's
// ContractSet so wiring reads Sessions: set.Sessions, etc.
type Deps struct {
	UnitOfWork  contracts.UnitOfWork
	Sessions    contracts.SessionStore
	Users       contracts.UserStore
	Credentials contracts.CredentialStore
	// Config and Permissions are direct (non-transactional) read handles, like
	// Sessions/Users/Credentials — used by read-only queries
	// (GetActiveConfigVersion, GetRolesForUser, …) that must not open a
	// UnitOfWork.
	Config      contracts.ConfigStore
	Permissions contracts.PermissionStore
	Nodes       contracts.NodeStore
	// Parts is the direct read handle for an item's playable parts. Writes
	// still go through the UnitOfWork; this exists because playback resolution
	// is a read that must not open a transaction (ADR 0045).
	Parts contracts.PartStore
	// PlaybackResolutions caches resolved locations per capability class
	// (ADR 0049). Optional: a Service built without one simply resolves through
	// the provider every time, which is what happened before the cache existed.
	PlaybackResolutions contracts.PlaybackResolutionStore
	// PlaybackStates is the direct read handle for where a viewer got to
	// (ADR 0046). Writes go through the UnitOfWork like every other mutation.
	PlaybackStates   contracts.PlaybackStateStore
	Clock            contracts.Clock
	IDs              contracts.IDGenerator
	ContentIDs       contracts.IDGenerator
	Policy           policy.PolicyDecisionPoint
	Events           contracts.EventPublisher
	PasswordVerifier domain.PasswordVerifier
	Capabilities     *CapabilityRegistry
	// Extensions is the runtime extension-module lifecycle (ADR 0081), injected
	// by the composition root. Optional: a Service built without one refuses
	// install and uninstall with Unavailable and reports no installed set, which
	// is the right behaviour for a test service or a build with extensions off.
	Extensions     ExtensionManager
	ModuleSettings contracts.ModuleSettingsStore
	// UserPreferences is the direct read handle for a user's own settings.
	// Writes go through the UnitOfWork like every other mutation.
	UserPreferences contracts.UserPreferenceStore
	// TelemetryQueries reads stored telemetry back for the expert-mode
	// surface (ADR 0058). Read-only and outside any transaction, like the
	// write side and for the mirror-image reason.
	TelemetryQueries contracts.TelemetryQueryStore
	// Tokens is the direct read handle for a session's bearer pair (ADR 0102):
	// validating an access token happens on every call and must not open a
	// transaction. Writes go through the UnitOfWork, where a pair and the
	// session it belongs to commit together.
	Tokens contracts.TokenStore
	// TelemetryMaintenance creates the partitions telemetry is written into
	// and drops the ones retention has run out on. It is what PurgeTelemetry
	// drives, and PurgeTelemetry is what the retention job calls.
	TelemetryMaintenance contracts.TelemetryMaintenanceStore
	// Jobs is the background-work queue (ADR 0017). Optional: a Service built
	// without one reports no jobs and refuses the job queries with
	// Unavailable, which is the honest answer for a build with no runner.
	Jobs contracts.JobStore
	// LibraryRules is the direct read handle for what the library should
	// contain (ADR 0104). Optional, like Jobs, and for the same reason.
	LibraryRules contracts.LibraryRuleStore
	// NodeMetadata is the direct read handle for stored descriptive metadata
	// (ADR 0107). Optional: without it nothing is stored and nothing is read,
	// and a detail falls back to what the node itself carries.
	NodeMetadata      contracts.NodeMetadataStore
	WatchAvailability contracts.WatchAvailabilityStore
	// Instance is the durable identity file (ADR 0098) — the one store that is
	// deliberately not PostgreSQL, so a server's name outlives its database.
	// Optional, and an absent one is a Platform with no name rather than a
	// failure.
	Instance contracts.InstanceIdentityStore
}

// NewService wires a Service to its Platform contracts, policy decision point
// and password verifier from d.
func NewService(d Deps) *Service {
	return &Service{
		uow:              d.UnitOfWork,
		sessionStore:     d.Sessions,
		users:            d.Users,
		credentials:      d.Credentials,
		configStore:      d.Config,
		permissions:      d.Permissions,
		moduleSettings:   d.ModuleSettings,
		userPreferences:  d.UserPreferences,
		telemetryQueries: d.TelemetryQueries,
		tokens:           d.Tokens,
		nodes:            d.Nodes,
		parts:            d.Parts,
		resolutions:      d.PlaybackResolutions,
		playbackStates:   d.PlaybackStates,
		clock:            d.Clock,
		ids:              d.IDs,
		contentIDs:       d.ContentIDs,
		policy:           d.Policy,
		events:           d.Events,
		passwordVerifier: d.PasswordVerifier,
		capabilities:     d.Capabilities,
		extensions:       d.Extensions,
		sessionManager:   sessions.NewManager(d.Clock, d.IDs),
		configManager:    config.NewManager(d.Clock, d.IDs, config.PlatformSchema()),

		telemetryMaintenance: d.TelemetryMaintenance,
		jobs:                 d.Jobs,
		libraryRules:         d.LibraryRules,
		nodeMetadata:         d.NodeMetadata,
		watchAvailability:    d.WatchAvailability,
		instance:             d.Instance,
		systemSession:        newSystemSessionRef(),
	}
}

// authenticate resolves the caller identity behind a credential. It is step 2
// of the command boundary and the equivalent gate for
// queries: it runs before any policy or state check, and failure stops
// processing immediately.
//
// The credential is an **access token** since ADR 0102, not a session id: it
// resolves to a session, is minutes-lived where the session is months-lived,
// and rotates where the session does not.
func (s *Service) authenticate(ctx context.Context, credential domain.SessionCredential) (domain.UserID, error) {
	session, err := s.sessionManager.Validate(ctx, s.sessionStore, s.tokens, credential)
	if err != nil {
		return "", err
	}
	return session.UserID, nil
}

// authenticateCaller is authenticate for the published content surface: a
// v1.Caller carries an opaque session reference (ADR 0017), which resolves to
// the same internal session identity as any other caller. The Caller is only
// as authoritative as that session, which this validates.
//
// The one reference that is not a session is the system principal, which
// resolvePrincipal recognises before any store is read — see
// system_principal.go.
func (s *Service) authenticateCaller(ctx context.Context, caller v1.Caller) (domain.UserID, error) {
	p, err := s.resolvePrincipal(ctx, caller)
	if err != nil {
		return "", err
	}
	return p.userID, nil
}

// authorize resolves step 3 of the command boundary (and the equivalent
// query gate): it asks the PolicyDecisionPoint whether subject may perform
// action on resource, translates a denial into a PermissionDenied contract
// error, and publishes an audit event for the denial. This is the
// enforcement point the deny-cannot-mutate-state
// guarantee depends on: every command and query calls this before opening
// a UnitOfWork or reading state.
func (s *Service) authorize(ctx context.Context, subject policy.Subject, action policy.Action, resource policy.Resource, policyContext policy.PolicyContext) error {
	// The one point every command and query passes through, so it is where the
	// *operation* gets named in a trace (ADR 0055, seam 4). A Connect span says
	// "Invoke"; this says which action Invoke dispatched to, which is the
	// difference between knowing a request happened and knowing what it did.
	//
	// This is the cheap half of seam 4. It does not bracket the handler's full
	// duration — that would mean a call at the top of each of twenty handlers —
	// but the expensive parts of a handler are already spanned beneath it: the
	// transaction (seam 5), its statements (seam 6) and any module it invokes
	// (seam 8). What remains unattributed is handler arithmetic.
	ctx, span := telemetry.Start(ctx, "authorize "+string(action),
		telemetry.String("action", string(action)),
		telemetry.String("resource", resource.Type))
	defer span.End()

	decision, err := s.policy.Authorize(ctx, subject, action, resource, policyContext)
	if err != nil {
		wrapped := contracts.WrapError(contracts.Internal, "evaluate policy", err)
		span.Fail(string(contracts.Internal), wrapped)
		return wrapped
	}
	if !decision.Allowed {
		s.publishAuditEvent(ctx, "authorization.denied", []byte(string(action)), string(subject.UserID))
		denied := contracts.NewError(contracts.PermissionDenied, decision.Reason)
		// A denial is a real outcome worth finding in a trace, not an
		// exceptional one — it is the single most useful span when someone
		// reports that a button does nothing.
		span.Fail(string(contracts.PermissionDenied), denied)
		return denied
	}
	return nil
}

// authorized is proof that a caller cleared both boundary gates —
// authenticate (step 2) and authorize (step 3) — for one action. It is the
// Platform's inside voice: a function taking an authorized is being called
// from within a handler that has already passed the boundary; a function
// taking a v1.Caller is an entry point that has not.
//
// Go has no annotations and no runtime proxies, so this guarantee cannot be
// woven in around a method the way a Java container weaves @PreAuthorize. It
// is carried in the type instead. The struct is unexported and enter is its
// only constructor, so a helper that requires one cannot be reached without
// the gates having run — and, the half that matters more here, cannot
// *repeat* them, because being inside is now something the signature can say.
//
// That is the defect this exists to close. Before it, an internal helper had
// no way to express "already inside", so it reached for the only shape
// available — a public Service method — and paid a full authenticate plus
// authorize per call. Ten search results meant ten session reads, ten policy
// evaluations and ten role reads for one user action, none of which could
// decide anything the one at the top of the handler had not already decided.
//
// The caller is retained rather than discarded because forwarding it into a
// module is legitimate and must stay possible: a module's own writes
// re-authorise as the invoking user (ADR 0017), which is the whole reason it
// is handed a Caller and not a Service with the boundary pre-cleared. That is
// a deliberate act at a module seam, not something a helper does by accident.
type authorized struct {
	userID domain.UserID
	caller v1.Caller
	// system marks work the Platform did on its own initiative rather than for
	// a person (ADR 0017). A handler reads it only to describe what it did —
	// an event actor, a log line — never to decide what it may do, which is
	// the policy decision point's answer and was already given.
	system bool
}

// enter runs the two boundary gates once and returns the proof. It is the
// entry-point preamble every handler shares, in one place, so the sequence
// authenticate-then-authorize is a property of this function rather than of
// each handler remembering it in the right order.
func (s *Service) enter(ctx context.Context, caller v1.Caller, action policy.Action, resource policy.Resource) (authorized, error) {
	p, err := s.resolvePrincipal(ctx, caller)
	if err != nil {
		return authorized{}, err
	}
	// The system principal goes through the same authorize call as anyone
	// else, carrying a flag the engine reads (ADR 0017). It is not a branch
	// around the gate: the decision is still the policy decision point's, it
	// is still spanned and still refusable, and moving it here would put an
	// authorization rule in the enforcement point.
	subject := policy.Subject{UserID: p.userID, System: p.system}
	if err := s.authorize(ctx, subject, action, resource, policy.PolicyContext{}); err != nil {
		return authorized{}, err
	}
	return authorized{userID: p.userID, caller: caller, system: p.system}, nil
}

// enterSession is enter for the handlers that take a raw domain.SessionID
// rather than the published v1.Caller — the users, roles, sessions and config
// families, which predate the content surface and were never part of it.
//
// **Those handlers' CallerSessionID fields carry an access token, not a session
// id** (ADR 0102). The field name and its type are unchanged because renaming
// fifteen fields and the hundred call sites that construct them is churn this
// change does not need — but the mismatch is named here, at the one place the
// conversion happens, rather than left for a reader to discover.
//
// Two forms rather than one because the two families genuinely differ at the
// signature: a v1.Caller is the opaque reference a module or client holds
// (ADR 0017), a SessionID is the Platform's own identifier. Converting one to
// the other here rather than at each call site keeps that distinction where it
// belongs — in the type the handler accepts — instead of scattering
// domain.SessionID(caller.Session) across twenty files.
func (s *Service) enterSession(ctx context.Context, session domain.SessionID, action policy.Action, resource policy.Resource) (authorized, error) {
	return s.enter(ctx, v1.Caller{Session: string(session)}, action, resource)
}

// newEvent builds an Event envelope for eventType with the
// given payload and actor, stamping a fresh id and both occurrence and record
// timestamps from the Service clock. In synchronous command handling
// OccurredAt and RecordedAt coincide. Audit events carry identifying data
// (usernames, session ids), so they default to RedactionSensitive — redacted
// from support bundles.
func (s *Service) newEvent(ctx context.Context, eventType string, payload []byte, actor string) domain.Event {
	now := s.clock.Now()
	// CorrelationID and CausationID carried a "empty until request-scoped
	// propagation exists" note from the day the envelope was written. This is
	// that propagation (ADR 0054): the correlation id is the trace id, so an
	// event row, the log lines around it, and the span that produced it share
	// one key — and no second identifier had to be invented to get there.
	//
	// A context with no trace yields empty ids, exactly as before. Background
	// work that has no request behind it should not manufacture one.
	tc, _ := telemetry.TraceFrom(ctx)
	return domain.Event{
		ID:             domain.EventID(s.ids.NewID()),
		Type:           eventType,
		OccurredAt:     now,
		RecordedAt:     now,
		Actor:          actor,
		CorrelationID:  tc.TraceIDString(),
		CausationID:    tc.SpanIDString(),
		Payload:        payload,
		RedactionClass: domain.RedactionSensitive,
	}
}

// publishAuditEvent publishes an audit event through the runtime event
// backbone. Publication is best-effort: a delivery failure
// must never mask the authorization or authentication outcome that
// triggered it, so the error is intentionally discarded.
func (s *Service) publishAuditEvent(ctx context.Context, eventType string, payload []byte, actor string) {
	_ = s.events.Publish(ctx, s.newEvent(ctx, eventType, payload, actor))
}
