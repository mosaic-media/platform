// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionModuleConfigure is the policy action evaluated for setting an optional
// module's user-managed settings.
const ActionModuleConfigure policy.Action = "module.configure"

// ConfigureModuleCommand sets the settings document for a registered optional
// module (ADR 0021). Settings is an opaque JSON document the Platform stores
// and hands back to the module without interpreting — an addon list, an API
// key, whatever the module reads.
type ConfigureModuleCommand struct {
	Caller   v1.Caller
	ModuleID string
	Settings []byte
}

// ConfigureModuleResult carries the stored settings.
type ConfigureModuleResult struct {
	ModuleID string
	Settings []byte
}

func validateConfigureModuleCommand(cmd ConfigureModuleCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.ModuleID == "" {
		return contracts.NewError(contracts.InvalidArgument, "module id is required")
	}
	if len(cmd.Settings) > 0 && !json.Valid(cmd.Settings) {
		return contracts.NewError(contracts.InvalidArgument, "settings must be a valid JSON document")
	}
	return nil
}

// ConfigureModule persists an optional module's settings, following the command
// boundary. It refuses an id that names no registered module, so a user cannot
// configure something that will never run.
func (s *Service) ConfigureModule(ctx context.Context, cmd ConfigureModuleCommand) (ConfigureModuleResult, error) {
	// 1. validate command shape.
	if err := validateConfigureModuleCommand(cmd); err != nil {
		return ConfigureModuleResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return ConfigureModuleResult{}, err
	}

	// 3. authorize the action.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionModuleConfigure, policy.Resource{Type: "module"}, policy.PolicyContext{}); err != nil {
		return ConfigureModuleResult{}, err
	}

	// 4. the module must be registered.
	if _, ok := s.lookupCapability(cmd.ModuleID); !ok {
		return ConfigureModuleResult{}, contracts.NewError(contracts.NotFound, "no module registered under id "+cmd.ModuleID)
	}

	settings := cmd.Settings
	if len(settings) == 0 {
		settings = []byte("{}")
	}

	// 5. persist the settings and the outbox event in one transaction.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		if _, err := tx.ModuleSettings().Set(ctx, domain.ModuleSettings{
			ModuleID: cmd.ModuleID, Settings: settings, UpdatedAt: s.clock.Now(),
		}); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent("module.configured", []byte(cmd.ModuleID), string(callerID)),
		})
	})
	if err != nil {
		return ConfigureModuleResult{}, err
	}

	// 6. return a Platform result type.
	return ConfigureModuleResult{ModuleID: cmd.ModuleID, Settings: settings}, nil
}
