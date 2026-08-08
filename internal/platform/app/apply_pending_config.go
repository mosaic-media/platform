// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// ApplyPendingConfig applies a configuration version that was waiting for an
// escalation, when the escalation that has just happened is enough to carry
// it.
//
// **It takes no caller, and that is deliberate rather than an omission.** The
// authorisation happened when a user asked for the version: that call went
// through the policy gate and left the request recorded as Pending. This runs
// at boot, on nobody's behalf, and its whole job is to finish what was already
// authorised — the same shape as a migration. A session gate here would ask a
// question with no one present to answer it.
//
// `granted` is what the caller can vouch for having done. A Platform that has
// just started grants Restart, because starting is exactly what it did; a
// Generation-class change is left waiting for the Supervisor rather than
// applied by a restart that was never going to carry it.
func (s *Service) ApplyPendingConfig(ctx context.Context, granted config.ReloadClass) (ActivateConfigVersionResult, error) {
	var outcome config.ActivationOutcome

	err := s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		var err error
		outcome, err = s.configManager.ApplyPending(ctx, tx.Config(), granted)
		if err != nil {
			return err
		}
		// Nothing was waiting: no event, because "the Platform booted and had
		// no pending configuration" is the ordinary case and an event per boot
		// would bury the ones that mean something.
		if outcome.Version.ID == "" {
			return nil
		}

		eventType := "config.activation_deferred"
		if outcome.Activated {
			eventType = "config.activated"
		}
		// No actor: this is the escalation completing, not a person acting.
		event := domain.OutboxEvent{Event: s.newEvent(ctx, eventType, []byte(string(outcome.Version.ID)), "")}
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
