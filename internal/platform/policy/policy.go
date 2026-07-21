// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package policy

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// Subject identifies the caller a decision is evaluated for, plus the
// session attributes a Policy Decision Point weighs under "subject"
// (roles, session strength, device trust).
type Subject struct {
	UserID       domain.UserID
	AuthStrength domain.AuthStrength
}

// Action identifies the operation being authorized, for example
// "user.create" or "user.session.revoke".
type Action string

// Resource identifies what an Action would act upon.
type Resource struct {
	Type string
	ID   string
}

// PolicyContext carries request-scoped ABAC attributes weighed under
// "context" — network origin, admin mode, recovery mode, and similar. It
// is intentionally sparse for this slice's simple rules.
type PolicyContext struct {
	AdminMode    bool
	RecoveryMode bool
}

// Decision is a Policy Decision Point's answer to one authorization
// request. Reason exists so denials remain explainable for auditability.
type Decision struct {
	Allowed bool
	Reason  string
}

// PolicyDecisionPoint answers authorize(subject, action, resource,
// context) with a Decision. It keeps the ABAC-ready shape regardless of
// how simple the underlying rules are.
type PolicyDecisionPoint interface {
	Authorize(ctx context.Context, subject Subject, action Action, resource Resource, policyContext PolicyContext) (Decision, error)
}
