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
	if err := validateActivateConfigVersionCommand(cmd); err != nil {
		return ActivateConfigVersionResult{}, err
	}

	az, err := s.enterSession(ctx, cmd.CallerSessionID, ActionConfigActivate,
		policy.Resource{Type: "config", ID: string(cmd.ConfigVersionID)})
	if err != nil {
		return ActivateConfigVersionResult{}, err
	}

	var outcome config.ActivationOutcome

	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		var err error
		outcome, err = s.configManager.Activate(ctx, tx.Config(), cmd.ConfigVersionID)
		if err != nil {
			return err
		}

		// The outbox event is appended whatever the outcome was.
		eventType := "config.activation_deferred"
		if outcome.Activated {
			eventType = "config.activated"
		}
		event := domain.OutboxEvent{Event: s.newEvent(ctx, eventType, []byte(string(outcome.Version.ID)), string(az.userID))}
		return tx.Outbox().Append(ctx, event)
	})
	if err != nil {
		return ActivateConfigVersionResult{}, err
	}

	return ActivateConfigVersionResult{
		Version:     outcome.Version,
		Activated:   outcome.Activated,
		ReloadClass: outcome.ReloadClass,
	}, nil
}
