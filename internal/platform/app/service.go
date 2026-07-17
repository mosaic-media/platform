package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/config"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	"github.com/mosaic-media/mosaic-platform/internal/platform/sessions"
)

// Service hosts Platform application command and query handlers. It holds
// direct read access to SessionStore, UserStore and CredentialStore for
// authentication and query paths, and a UnitOfWork for the transactional
// write path — the same contracts, reached through the shape appropriate
// to each operation (MEG-015 §04). It is the enforcement point for policy
// decisions: the policy.PolicyDecisionPoint only decides; Service is what
// actually refuses to mutate state on a deny (MEG-015 §07).
type Service struct {
	uow              contracts.UnitOfWork
	sessionStore     contracts.SessionStore
	users            contracts.UserStore
	credentials      contracts.CredentialStore
	configStore      contracts.ConfigStore
	permissions      contracts.PermissionStore
	clock            contracts.Clock
	ids              contracts.IDGenerator
	policy           policy.PolicyDecisionPoint
	events           contracts.EventPublisher
	passwordVerifier domain.PasswordVerifier
	sessionManager   *sessions.Manager
	configManager    *config.Manager
}

// NewService wires a Service to its Platform contracts, policy decision
// point and password verifier. configStore and permissions are direct
// (non-transactional) read handles, mirroring sessionStore/users/
// credentials — used by read-only queries (GetActiveConfigVersion,
// GetRolesForUser, ...) that must not open a UnitOfWork (MEG-015 §04).
func NewService(
	uow contracts.UnitOfWork,
	sessionStore contracts.SessionStore,
	users contracts.UserStore,
	credentials contracts.CredentialStore,
	configStore contracts.ConfigStore,
	permissions contracts.PermissionStore,
	clock contracts.Clock,
	ids contracts.IDGenerator,
	policyEngine policy.PolicyDecisionPoint,
	events contracts.EventPublisher,
	passwordVerifier domain.PasswordVerifier,
) *Service {
	return &Service{
		uow:              uow,
		sessionStore:     sessionStore,
		users:            users,
		credentials:      credentials,
		configStore:      configStore,
		permissions:      permissions,
		clock:            clock,
		ids:              ids,
		policy:           policyEngine,
		events:           events,
		passwordVerifier: passwordVerifier,
		sessionManager:   sessions.NewManager(clock, ids),
		configManager:    config.NewManager(clock, ids, config.PlatformSchema()),
	}
}

// authenticate resolves the caller identity behind sessionID. It is step 2
// of the command boundary (MEG-015 §04) and the equivalent gate for
// queries: it runs before any policy or state check, and failure stops
// processing immediately (MEG-009 §03 — Authentication).
func (s *Service) authenticate(ctx context.Context, sessionID domain.SessionID) (domain.UserID, error) {
	session, err := s.sessionManager.Validate(ctx, s.sessionStore, sessionID)
	if err != nil {
		return "", err
	}
	return session.UserID, nil
}

// authorize resolves step 3 of the command boundary (and the equivalent
// query gate): it asks the PolicyDecisionPoint whether subject may perform
// action on resource, translates a denial into a PermissionDenied contract
// error, and publishes an audit event for the denial (MEG-015 §07 — Audit
// Events). This is the enforcement point the deny-cannot-mutate-state
// guarantee depends on: every command and query calls this before opening
// a UnitOfWork or reading state.
func (s *Service) authorize(ctx context.Context, subject policy.Subject, action policy.Action, resource policy.Resource, policyContext policy.PolicyContext) error {
	decision, err := s.policy.Authorize(ctx, subject, action, resource, policyContext)
	if err != nil {
		return contracts.WrapError(contracts.Internal, "evaluate policy", err)
	}
	if !decision.Allowed {
		s.publishAuditEvent(ctx, "authorization.denied", []byte(string(action)), string(subject.UserID))
		return contracts.NewError(contracts.PermissionDenied, decision.Reason)
	}
	return nil
}

// newEvent builds an Event envelope (MEG-015 §06) for eventType with the
// given payload and actor, stamping a fresh id and both occurrence and record
// timestamps from the Service clock. In synchronous command handling
// OccurredAt and RecordedAt coincide. Audit events carry identifying data
// (usernames, session ids), so they default to RedactionSensitive — redacted
// from support bundles (MEG-015 §07/§09).
func (s *Service) newEvent(eventType string, payload []byte, actor string) domain.Event {
	now := s.clock.Now()
	return domain.Event{
		ID:             domain.EventID(s.ids.NewID()),
		Type:           eventType,
		OccurredAt:     now,
		RecordedAt:     now,
		Actor:          actor,
		Payload:        payload,
		RedactionClass: domain.RedactionSensitive,
	}
}

// publishAuditEvent publishes an audit event through the runtime event
// backbone (MEG-015 §07). Publication is best-effort: a delivery failure
// must never mask the authorization or authentication outcome that
// triggered it, so the error is intentionally discarded.
func (s *Service) publishAuditEvent(ctx context.Context, eventType string, payload []byte, actor string) {
	_ = s.events.Publish(ctx, s.newEvent(eventType, payload, actor))
}
