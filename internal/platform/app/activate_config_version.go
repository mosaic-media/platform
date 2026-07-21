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
)

// ActionConfigActivate is the policy action evaluated for
// ActivateConfigVersion.
const ActionConfigActivate policy.Action = "config.activate"

// ActivateConfigVersionCommand runs the Activate step of the activation
// state machine against a Validated version.
type ActivateConfigVersionCommand struct {
	CallerSessionID domain.SessionID
	ConfigVersionID domain.ConfigVersionID
}

// ActivateConfigVersionResult is the Platform result type returned once
// the activate step has run. Activated is true only when the change was
// Hot-classified and applied immediately; otherwise the version remains
// Validated and ReloadClass reports what escalation (restart, a new
// Generation, or the recovery flow) it requires before it can take effect
// — the Platform correctly classifies and flags the change here, but does
// not perform that escalation itself yet (the Supervisor handoff is a
// later slice).
type ActivateConfigVersionResult struct {
	Version     domain.ConfigVersion
	Activated   bool
	ReloadClass config.ReloadClass
}

func validateActivateConfigVersionCommand(cmd ActivateConfigVersionCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.ConfigVersionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "config version id is required")
	}
	return nil
}

// ActivateConfigVersion implements the command order for the Activate
// step of the activation state machine.
func (s *Service) ActivateConfigVersion(ctx context.Context, cmd ActivateConfigVersionCommand) (ActivateConfigVersionResult, error) {
	// 1. validate command shape.
	if err := validateActivateConfigVersionCommand(cmd); err != nil {
		return ActivateConfigVersionResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return ActivateConfigVersionResult{}, err
	}

	// 3. authorize action through policy.
	resource := policy.Resource{Type: "config", ID: string(cmd.ConfigVersionID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionConfigActivate, resource, policy.PolicyContext{}); err != nil {
		return ActivateConfigVersionResult{}, err
	}

	var outcome config.ActivationOutcome

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. load state and apply the activate/defer domain rule.
		var err error
		outcome, err = s.configManager.Activate(ctx, tx.Config(), cmd.ConfigVersionID)
		if err != nil {
			return err
		}

		// 7. persist the outbox event alongside whatever the outcome was.
		eventType := "config.activation_deferred"
		if outcome.Activated {
			eventType = "config.activated"
		}
		event := domain.OutboxEvent{Event: s.newEvent(eventType, []byte(string(outcome.Version.ID)), string(callerID))}
		return tx.Outbox().Append(ctx, event)
	})
	if err != nil {
		return ActivateConfigVersionResult{}, err
	}

	// 8. return a Platform result type.
	return ActivateConfigVersionResult{
		Version:     outcome.Version,
		Activated:   outcome.Activated,
		ReloadClass: outcome.ReloadClass,
	}, nil
}
