package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// Subject identifies the caller a policy decision is evaluated for.
type Subject struct {
	UserID domain.UserID
}

// Action identifies the operation being authorized, for example
// "user.create" or "user.read".
type Action string

// Resource identifies what an Action would act upon.
type Resource struct {
	Type string
	ID   string
}

// PolicyContext carries request-scoped ABAC attributes (device, network,
// authentication strength, ...). It is empty until the real policy engine
// lands with the Identity, sessions and policy slice (MEG-015 §12).
type PolicyContext struct{}

// Decision is a Policy Decision Point's answer to one authorization
// request.
type Decision struct {
	Allowed bool
	Reason  string
}

// PolicyDecisionPoint is the policy boundary application services call
// before mutating or reading state. It keeps the ABAC-ready shape required
// by MEG-009 §04 — Authorisation: every decision evaluates Subject, Action,
// Resource and Context as distinct inputs, even while the decision logic
// itself is a stand-in.
type PolicyDecisionPoint interface {
	Authorize(ctx context.Context, subject Subject, action Action, resource Resource, policyContext PolicyContext) (Decision, error)
}

// StubPolicy is a placeholder PolicyDecisionPoint. It renders a fixed
// decision for every request rather than evaluating roles, relationships
// or attributes. It exists only to prove the application boundary's
// authorization step is wired correctly; the real hybrid RBAC/ReBAC/ABAC
// engine arrives with the Identity, sessions and policy slice (MEG-015
// §12, MEG-009 §04).
//
// Its zero value denies every request, matching the default-deny rule in
// MEG-009 §04.
type StubPolicy struct {
	Allow bool
}

func (p StubPolicy) Authorize(_ context.Context, _ Subject, _ Action, _ Resource, _ PolicyContext) (Decision, error) {
	if p.Allow {
		return Decision{Allowed: true, Reason: "stub policy: allow"}, nil
	}
	return Decision{Allowed: false, Reason: "stub policy: default deny"}, nil
}
