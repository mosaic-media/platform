// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// ActionConfigValidate is the policy action evaluated for
// ValidateConfigVersion.
const ActionConfigValidate policy.Action = "config.validate"

// ValidateConfigVersionCommand runs the Validate step of the
// activation state machine against a Draft version.
type ValidateConfigVersionCommand struct {
	CallerSessionID domain.SessionID
	ConfigVersionID domain.ConfigVersionID
}

// ValidateConfigVersionResult is the Platform result type returned once
// validation has run. Version.Status is either ConfigValidated or
// ConfigRejected — both are a successful command outcome.
type ValidateConfigVersionResult struct {
	Version domain.ConfigVersion
}

func validateValidateConfigVersionCommand(cmd ValidateConfigVersionCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if cmd.ConfigVersionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "config version id is required")
	}
	return nil
}

// ValidateConfigVersion implements the command boundary for the Validate
// step of the activation state machine.
func (s *Service) ValidateConfigVersion(ctx context.Context, cmd ValidateConfigVersionCommand) (ValidateConfigVersionResult, error) {
	// 1. validate command shape.
	if err := validateValidateConfigVersionCommand(cmd); err != nil {
		return ValidateConfigVersionResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return ValidateConfigVersionResult{}, err
	}

	// 3. authorize action through policy.
	resource := policy.Resource{Type: "config", ID: string(cmd.ConfigVersionID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionConfigValidate, resource, policy.PolicyContext{}); err != nil {
		return ValidateConfigVersionResult{}, err
	}

	var result ValidateConfigVersionResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. load state and apply the validate/reject domain rule.
		version, err := s.configManager.Validate(ctx, tx.Config(), cmd.ConfigVersionID)
		if err != nil {
			return err
		}

		// 7. persist the outbox event alongside the status transition.
		eventType := "config.validated"
		if version.Status == domain.ConfigRejected {
			eventType = "config.rejected"
		}
		event := domain.OutboxEvent{Event: s.newEvent(eventType, []byte(string(version.ID)), string(callerID))}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}

		result = ValidateConfigVersionResult{Version: version}
		return nil
	})
	if err != nil {
		return ValidateConfigVersionResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
