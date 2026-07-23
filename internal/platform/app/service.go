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
	nodes            contracts.NodeStore
	parts            contracts.PartStore
	clock            contracts.Clock
	ids              contracts.IDGenerator
	contentIDs       contracts.IDGenerator
	policy           policy.PolicyDecisionPoint
	events           contracts.EventPublisher
	passwordVerifier domain.PasswordVerifier
	capabilities     *CapabilityRegistry
	sessionManager   *sessions.Manager
	configManager    *config.Manager
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
	Parts            contracts.PartStore
	Clock            contracts.Clock
	IDs              contracts.IDGenerator
	ContentIDs       contracts.IDGenerator
	Policy           policy.PolicyDecisionPoint
	Events           contracts.EventPublisher
	PasswordVerifier domain.PasswordVerifier
	Capabilities     *CapabilityRegistry
	ModuleSettings   contracts.ModuleSettingsStore
	// UserPreferences is the direct read handle for a user's own settings.
	// Writes go through the UnitOfWork like every other mutation.
	UserPreferences contracts.UserPreferenceStore
	// TelemetryQueries reads stored telemetry back for the expert-mode
	// surface (ADR 0058). Read-only and outside any transaction, like the
	// write side and for the mirror-image reason.
	TelemetryQueries contracts.TelemetryQueryStore
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
		nodes:            d.Nodes,
		parts:            d.Parts,
		clock:            d.Clock,
		ids:              d.IDs,
		contentIDs:       d.ContentIDs,
		policy:           d.Policy,
		events:           d.Events,
		passwordVerifier: d.PasswordVerifier,
		capabilities:     d.Capabilities,
		sessionManager:   sessions.NewManager(d.Clock, d.IDs),
		configManager:    config.NewManager(d.Clock, d.IDs, config.PlatformSchema()),
	}
}

// authenticate resolves the caller identity behind sessionID. It is step 2
// of the command boundary and the equivalent gate for
// queries: it runs before any policy or state check, and failure stops
// processing immediately.
func (s *Service) authenticate(ctx context.Context, sessionID domain.SessionID) (domain.UserID, error) {
	session, err := s.sessionManager.Validate(ctx, s.sessionStore, sessionID)
	if err != nil {
		return "", err
	}
	return session.UserID, nil
}

// authenticateCaller is authenticate for the published content surface: a
// v1.Caller carries an opaque session reference (ADR 0017), which resolves to
// the same internal session identity as any other caller. The Caller is only
// as authoritative as that session, which this validates.
func (s *Service) authenticateCaller(ctx context.Context, caller v1.Caller) (domain.UserID, error) {
	return s.authenticate(ctx, domain.SessionID(caller.Session))
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
