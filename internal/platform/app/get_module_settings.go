// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionModuleRead is the policy action evaluated for reading an optional
// module's settings. Settings may hold secrets (an addon URL with a token), so
// reads are gated too, not only writes.
const ActionModuleRead policy.Action = "module.read"

// GetModuleSettingsQuery reads a module's settings document.
type GetModuleSettingsQuery struct {
	Caller   v1.Caller
	ModuleID string
}

// GetModuleSettingsResult carries the settings document, an empty object when
// the module has none yet.
type GetModuleSettingsResult struct {
	ModuleID string
	Settings []byte
}

// GetModuleSettings reads an optional module's settings. Like every query it
// authenticates and authorises but opens no transaction.
func (s *Service) GetModuleSettings(ctx context.Context, query GetModuleSettingsQuery) (GetModuleSettingsResult, error) {
	if query.Caller.Session == "" {
		return GetModuleSettingsResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if query.ModuleID == "" {
		return GetModuleSettingsResult{}, contracts.NewError(contracts.InvalidArgument, "module id is required")
	}

	callerID, err := s.authenticateCaller(ctx, query.Caller)
	if err != nil {
		return GetModuleSettingsResult{}, err
	}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionModuleRead, policy.Resource{Type: "module"}, policy.PolicyContext{}); err != nil {
		return GetModuleSettingsResult{}, err
	}

	settings, err := s.readModuleSettings(ctx, query.ModuleID)
	if err != nil {
		return GetModuleSettingsResult{}, err
	}
	return GetModuleSettingsResult{ModuleID: query.ModuleID, Settings: settings}, nil
}
