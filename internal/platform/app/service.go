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
// authentication and query paths, and a UnitOfWork for the transactional write
// path — the same contracts, reached through the shape appropriate to each
// operation. It is the enforcement point for policy decisions: the
// policy.PolicyDecisionPoint only decides; Service is what refuses to mutate
// state on a deny.
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
	// metrics is the live meter collector (sdk#9). Nil in a Service built
	// without one, which is every test that does not exercise diagnostics.
	metrics          *telemetry.MetricCollector
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
	// lives in (platform#36). Optional: a Service built without one refuses
	// PurgeTelemetry with Unavailable rather than reporting a sweep that did
	// not happen.
	telemetryMaintenance contracts.TelemetryMaintenanceStore
	// jobs is the background-work queue (platform#13's no-user case). Optional
	// for the same reason.
	jobs contracts.JobStore
	// libraryRules is the direct read handle for what the library should
	// contain (platform#60). Writes go through the UnitOfWork like every other
	// mutation. Optional: a Service built without one reports no rules and
	// refuses to write any, which is what makes the maintenance handler a
	// no-op rather than a panic in a build with no store.
	libraryRules contracts.LibraryRuleStore
	// nodeMetadata is the direct read handle for what a provider said about a
	// materialised title (platform#62). Optional: a Service built without one
	// renders a library detail from the node alone.
	nodeMetadata contracts.NodeMetadataStore
	// snapshots is the direct handle for the last good answer a source gave
	// (platform#30) — what a source-backed screen renders from when the source
	// is slow, cold or down. Optional: a Service built without one asks its
	// providers on every render and shows an empty screen when they all fail.
	snapshots contracts.SourceSnapshotStore
	// watchAvailability is the direct handle for the queryable projection of
	// where a work can be watched (roadmap M2.5). Optional: a Service built
	// without one stores no availability and offers no streaming-service facet.
	watchAvailability contracts.WatchAvailabilityStore
	// issues is the resolution register (platform#74) — what is wrong with this
	// install, now. Optional: a Service built without one reports an empty
	// register and records nothing. It is a direct handle rather than
	// transactional because the detectors are boot paths and background work,
	// not somebody's command.
	issues contracts.IssueStore
	// upgrades records what somebody asked the Supervisor to install
	// (platform#77). The Platform cannot perform an upgrade, so this is a
	// request rather than an action.
	upgrades contracts.UpgradeStore
	// instance is what this install calls itself, held outside PostgreSQL
	// (platform#54). Optional: a Service built without one has no name to
	// report and records none when a server is claimed.
	instance contracts.InstanceIdentityStore

	// systemSession is the opaque reference SystemCaller hands out. Minted per
	// process in NewService — see system_principal.go for why it is random
	// rather than a well-known constant.
	systemSession string
}

// Deps are the collaborators a Service is built from. They are passed as a
// named struct rather than positionally because several members share a type:
// IDs and ContentIDs are both contracts.IDGenerator, and swapping them would
// compile cleanly while silently crossing the platform and content id
// generators. Field names mirror the composition root's ContractSet so wiring
// reads Sessions: set.Sessions, etc.
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
	// is a read that must not open a transaction (platform#25).
	Parts contracts.PartStore
	// PlaybackResolutions caches resolved locations per capability class
	// (platform#28). Optional: a Service built without one resolves through the
	// provider every time.
	PlaybackResolutions contracts.PlaybackResolutionStore
	// PlaybackStates is the direct read handle for where a viewer got to
	// (platform#26). Writes go through the UnitOfWork like every other mutation.
	PlaybackStates   contracts.PlaybackStateStore
	Clock            contracts.Clock
	IDs              contracts.IDGenerator
	ContentIDs       contracts.IDGenerator
	Policy           policy.PolicyDecisionPoint
	Events           contracts.EventPublisher
	PasswordVerifier domain.PasswordVerifier
	Capabilities     *CapabilityRegistry
	// Extensions is the runtime extension-module lifecycle (platform#51),
	// injected by the composition root. Optional: a Service built without one
	// refuses install and uninstall with Unavailable and reports no installed
	// set.
	Extensions     ExtensionManager
	ModuleSettings contracts.ModuleSettingsStore
	// UserPreferences is the direct read handle for a user's own settings.
	// Writes go through the UnitOfWork like every other mutation.
	UserPreferences contracts.UserPreferenceStore
	// TelemetryQueries reads stored telemetry back for the expert-mode
	// surface (platform#36). Read-only and outside any transaction, like the
	// write side and for the mirror-image reason.
	TelemetryQueries contracts.TelemetryQueryStore
	// Metrics is the process's meter collector, read by ListMetrics (sdk#9).
	Metrics *telemetry.MetricCollector
	// Tokens is the direct read handle for a session's bearer pair (platform#58):
	// validating an access token happens on every call and must not open a
	// transaction. Writes go through the UnitOfWork, where a pair and the
	// session it belongs to commit together.
	Tokens contracts.TokenStore
	// TelemetryMaintenance creates the partitions telemetry is written into
	// and drops the ones retention has run out on. It is what PurgeTelemetry
	// drives, and PurgeTelemetry is what the retention job calls.
	TelemetryMaintenance contracts.TelemetryMaintenanceStore
	// Jobs is the background-work queue (platform#13). Optional: a Service
	// built without one reports no jobs and refuses the job queries with
	// Unavailable.
	Jobs contracts.JobStore
	// LibraryRules is the direct read handle for what the library should
	// contain (platform#60). Optional, like Jobs, and for the same reason.
	LibraryRules contracts.LibraryRuleStore
	// Issues is the resolution register: what is wrong with this install, now
	// (platform#74). contracts.IssueStore documents the idempotence rule.
	Issues contracts.IssueStore
	// Upgrades is where a request for a version is recorded for the Supervisor
	// to carry out (platform#77). Nil is a build with no upgrade path, where
	// the suggestion is refused rather than silently doing nothing.
	Upgrades contracts.UpgradeStore
	// NodeMetadata is the direct read handle for stored descriptive metadata
	// (platform#62). Optional: without it nothing is stored and nothing is
	// read, and a detail falls back to what the node itself carries.
	NodeMetadata contracts.NodeMetadataStore
	// Snapshots is the direct handle for the last good answer a source gave
	// (platform#30). Optional: without it every source-backed screen asks its
	// providers live and renders nothing when they all fail.
	Snapshots         contracts.SourceSnapshotStore
	WatchAvailability contracts.WatchAvailabilityStore
	// Instance is the durable identity file (platform#54) — the one store that
	// is deliberately not PostgreSQL, so a server's name outlives its database.
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
		metrics:          d.Metrics,
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
		issues:               d.Issues,
		upgrades:             d.Upgrades,
		snapshots:            d.Snapshots,
		watchAvailability:    d.WatchAvailability,
		instance:             d.Instance,
		systemSession:        newSystemSessionRef(),
	}
}

// authenticate resolves the caller identity behind a credential. It is step 2
// of the command boundary and the equivalent gate for queries: it runs before
// any policy or state check, and failure stops processing immediately.
//
// The credential is an access token since platform#58, not a session id: it
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
// v1.Caller carries an opaque session reference (platform#13), which resolves to
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

// authorize resolves step 3 of the command boundary (and the equivalent query
// gate): it asks the PolicyDecisionPoint whether subject may perform action on
// resource, translates a denial into a PermissionDenied contract error, and
// publishes an audit event for the denial. Every command and query must call
// this before opening a UnitOfWork or reading state; that ordering is what the
// deny-cannot-mutate-state guarantee rests on.
func (s *Service) authorize(ctx context.Context, subject policy.Subject, action policy.Action, resource policy.Resource, policyContext policy.PolicyContext) error {
	// The one point every command and query passes through, so it is where the
	// operation gets named in a trace (platform#33, seam 4). A Connect span
	// says "Invoke"; this says which action Invoke dispatched to.
	//
	// The span does not bracket the handler's full duration, only the policy
	// evaluation. The expensive parts of a handler are spanned beneath it
	// anyway: the transaction (seam 5), its statements (seam 6) and any module
	// it invokes (seam 8). What remains unattributed is handler arithmetic.
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
		// A denial is failed on the span deliberately: it is an ordinary
		// outcome, and the one span worth finding when someone reports that a
		// button does nothing.
		span.Fail(string(contracts.PermissionDenied), denied)
		return denied
	}
	return nil
}

// authorized is proof that a caller cleared both boundary gates —
// authenticate (step 2) and authorize (step 3) — for one action. A function
// taking an authorized is being called from within a handler that has already
// passed the boundary; a function taking a v1.Caller is an entry point that
// has not.
//
// The struct is unexported and enter is its only constructor, so a helper that
// requires one cannot be reached without the gates having run — and cannot
// repeat them, because being inside the boundary is something the signature
// can now say.
//
// Do not call a public Service method from inside a handler to reach its
// logic: that re-runs the whole boundary. Take an authorized and read the
// stores directly instead. One search returning ten results otherwise costs
// ten session reads, ten policy evaluations and ten role reads, none of which
// can decide anything the gate at the top of the handler has not.
//
// The caller is retained rather than discarded because forwarding it into a
// module must stay possible: a module's own writes re-authorise as the
// invoking user (platform#13), which is why it is handed a Caller and not a
// Service with the boundary pre-cleared.
type authorized struct {
	userID domain.UserID
	caller v1.Caller
	// system marks work the Platform did on its own initiative rather than for
	// a person (platform#13). A handler reads it only to describe what it did —
	// an event actor, a log line — never to decide what it may do, which is the
	// policy decision point's answer and was already given.
	system bool
}

// enter runs the two boundary gates once and returns the proof. It is the
// entry-point preamble every handler shares, so the sequence
// authenticate-then-authorize is a property of this function rather than of
// each handler remembering it in the right order.
func (s *Service) enter(ctx context.Context, caller v1.Caller, action policy.Action, resource policy.Resource) (authorized, error) {
	p, err := s.resolvePrincipal(ctx, caller)
	if err != nil {
		return authorized{}, err
	}
	// The system principal goes through the same authorize call as anyone
	// else, carrying a flag the engine reads (platform#13). It is not a branch
	// around the gate: the decision stays the policy decision point's, still
	// spanned and still refusable. Short-circuiting it here would put an
	// authorization rule in the enforcement point.
	subject := policy.Subject{UserID: p.userID, System: p.system}
	if err := s.authorize(ctx, subject, action, resource, policy.PolicyContext{}); err != nil {
		return authorized{}, err
	}
	return authorized{userID: p.userID, caller: caller, system: p.system}, nil
}

// enterSession is enter for the handlers that take a raw domain.SessionID
// rather than the published v1.Caller — the users, roles, sessions and config
// families, which predate the content surface.
//
// Those handlers' CallerSessionID fields carry an access token, not a session
// id (platform#58). The field name and its type were left alone; the mismatch
// is named here, at the one place the conversion happens.
//
// Two forms rather than one because the families differ at the signature: a
// v1.Caller is the opaque reference a module or client holds (platform#13), a
// SessionID is the Platform's own identifier. Converting here rather than at
// each call site keeps domain.SessionID(caller.Session) out of twenty files.
func (s *Service) enterSession(ctx context.Context, session domain.SessionID, action policy.Action, resource policy.Resource) (authorized, error) {
	return s.enter(ctx, v1.Caller{Session: string(session)}, action, resource)
}

// newEvent builds an Event envelope for eventType with the given payload and
// actor, stamping a fresh id and both occurrence and record timestamps from the
// Service clock. In synchronous command handling OccurredAt and RecordedAt
// coincide. Audit events carry identifying data (usernames, session ids), so
// they default to RedactionSensitive — redacted from support bundles.
func (s *Service) newEvent(ctx context.Context, eventType string, payload []byte, actor string) domain.Event {
	now := s.clock.Now()
	// The correlation id is the trace id (platform#32), so an event row, the
	// log lines around it and the span that produced it share one key.
	//
	// A context with no trace yields empty ids. Background work with no request
	// behind it should not manufacture one.
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
// backbone. Publication is best-effort: a delivery failure must never mask the
// authorization or authentication outcome that triggered it, so the error is
// discarded deliberately.
func (s *Service) publishAuditEvent(ctx context.Context, eventType string, payload []byte, actor string) {
	_ = s.events.Publish(ctx, s.newEvent(ctx, eventType, payload, actor))
}
