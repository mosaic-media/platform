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

// ActionConfigDraft is the policy action evaluated for DraftConfigVersion.
const ActionConfigDraft policy.Action = "config.draft"

// DraftConfigVersionCommand saves a new, unvalidated configuration
// candidate. The Platform exposes this as a callable service
// so GraphQL or recovery tooling can drive it later; there is no admin UI
// yet.
type DraftConfigVersionCommand struct {
	CallerSessionID domain.SessionID
	Payload         []byte
}

// DraftConfigVersionResult is the Platform result type returned once the
// draft has committed.
type DraftConfigVersionResult struct {
	Version domain.ConfigVersion
}

func validateDraftConfigVersionCommand(cmd DraftConfigVersionCommand) error {
	if cmd.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if len(cmd.Payload) == 0 {
		return contracts.NewError(contracts.InvalidArgument, "config payload is required")
	}
	return nil
}

// DraftConfigVersion implements the command order for
// the Draft step of the activation state machine.
func (s *Service) DraftConfigVersion(ctx context.Context, cmd DraftConfigVersionCommand) (DraftConfigVersionResult, error) {
	// 1. validate command shape.
	if err := validateDraftConfigVersionCommand(cmd); err != nil {
		return DraftConfigVersionResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticate(ctx, cmd.CallerSessionID)
	if err != nil {
		return DraftConfigVersionResult{}, err
	}

	// 3. authorize action through policy.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionConfigDraft, policy.Resource{Type: "config"}, policy.PolicyContext{}); err != nil {
		return DraftConfigVersionResult{}, err
	}

	var result DraftConfigVersionResult

	// 4. open a UnitOfWork.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		// 5-6. load state and apply the domain rule (draft creation) via the
		// config state machine.
		version, err := s.configManager.Draft(ctx, tx.Config(), cmd.Payload)
		if err != nil {
			return err
		}

		// 7. persist state and outbox events in the same transaction.
		event := domain.OutboxEvent{Event: s.newEvent("config.drafted", []byte(string(version.ID)), string(callerID))}
		if err := tx.Outbox().Append(ctx, event); err != nil {
			return err
		}

		result = DraftConfigVersionResult{Version: version}
		return nil
	})
	if err != nil {
		return DraftConfigVersionResult{}, err
	}

	// 8. return a Platform result type.
	return result, nil
}
