// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ModuleSettingsUIQuery asks a module for its own settings screen (ADR 0038).
type ModuleSettingsUIQuery struct {
	Caller   v1.Caller
	ModuleID string
}

// ModuleSettingsUIResult carries the module's settings screen as a serialised
// UINode tree (JSON), validated by the Platform before it leaves the boundary.
type ModuleSettingsUIResult struct {
	ModuleID string
	UI       []byte
}

// ModuleSettingsUI resolves a module's contributed settings screen (ADR 0038): a
// module that fills RoleSettingsUI renders its own configuration UI as SDUI, and
// the Platform hosts it. Like every query it authenticates and authorises (a
// settings read — ActionModuleRead), reads the module's current settings so the
// module can render them, invokes the provider, and validates the returned
// UINode before returning it. Nothing here writes; the screen's own actions run
// configureModule to persist changes.
func (s *Service) ModuleSettingsUI(ctx context.Context, query ModuleSettingsUIQuery) (ModuleSettingsUIResult, error) {
	if query.Caller.Session == "" {
		return ModuleSettingsUIResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if query.ModuleID == "" {
		return ModuleSettingsUIResult{}, contracts.NewError(contracts.InvalidArgument, "module id is required")
	}

	if _, err := s.enter(ctx, query.Caller, ActionModuleRead, policy.Resource{Type: "module"}); err != nil {
		return ModuleSettingsUIResult{}, err
	}

	provider, ok := s.capabilitySettingsUIProvider(query.ModuleID)
	if !ok {
		return ModuleSettingsUIResult{}, contracts.NewError(contracts.NotFound, "no settings UI provider registered under id "+query.ModuleID)
	}
	settings, err := s.readModuleSettings(ctx, query.ModuleID)
	if err != nil {
		return ModuleSettingsUIResult{}, err
	}

	ctx, span := moduleSpan(ctx, query.ModuleID, "settings_ui")
	resp, err := provider.SettingsUI(ctx, v1.SettingsUIRequest{Caller: query.Caller, Settings: settings})
	failSpan(span, err)
	span.End()
	if err != nil {
		return ModuleSettingsUIResult{}, contracts.WrapError(contracts.Unavailable, "module settings UI", err)
	}
	if err := validateUINode(resp.UI); err != nil {
		return ModuleSettingsUIResult{}, contracts.WrapError(contracts.Internal, "module settings UI is not a valid UINode", err)
	}
	return ModuleSettingsUIResult{ModuleID: query.ModuleID, UI: resp.UI}, nil
}

// SettingsModule is one module that contributes a settings screen, for the
// settings index.
type SettingsModule struct {
	ModuleID string
	Name     string
}

// ListSettingsModulesQuery asks which modules have a settings screen to open.
type ListSettingsModulesQuery struct {
	Caller v1.Caller
}

// ListSettingsModulesResult carries them in stable module-id order.
type ListSettingsModulesResult struct {
	Modules []SettingsModule
}

// ListSettingsModules enumerates the modules that fill RoleSettingsUI (ADR
// 0038), so the settings host can offer an index rather than naming one module
// by constant.
//
// It authorises the same read as opening one of those screens — a caller who may
// not read a module's settings must not learn which modules are installed from
// the index either. Nothing here invokes a module: the list is the registry's,
// and a module is only asked to render when a user opens it.
func (s *Service) ListSettingsModules(ctx context.Context, query ListSettingsModulesQuery) (ListSettingsModulesResult, error) {
	if query.Caller.Session == "" {
		return ListSettingsModulesResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if _, err := s.enter(ctx, query.Caller, ActionModuleRead, policy.Resource{Type: "module"}); err != nil {
		return ListSettingsModulesResult{}, err
	}
	if s.capabilities == nil {
		return ListSettingsModulesResult{}, nil
	}
	entries := s.capabilities.SettingsUIProviders()
	modules := make([]SettingsModule, 0, len(entries))
	for _, e := range entries {
		modules = append(modules, SettingsModule{ModuleID: e.ModuleID, Name: e.Name})
	}
	return ListSettingsModulesResult{Modules: modules}, nil
}

// capabilitySettingsUIProvider resolves a settings-UI provider by module id,
// tolerating a Service built without a registry.
func (s *Service) capabilitySettingsUIProvider(id string) (v1.SettingsUIProvider, bool) {
	if s.capabilities == nil {
		return nil, false
	}
	return s.capabilities.SettingsUIProvider(id)
}

// validateUINode confines a module-supplied settings screen to a well-formed
// UINode tree before the Platform hosts it (ADR 0038): the bytes must be a JSON
// object carrying a non-empty string "type". Full schema validation is a later
// hardening; this catches a malformed or non-node payload at the boundary.
func validateUINode(ui []byte) error {
	if len(ui) == 0 {
		return contracts.NewError(contracts.InvalidArgument, "empty settings UI")
	}
	var node struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(ui, &node); err != nil {
		return err
	}
	if node.Type == "" {
		return contracts.NewError(contracts.InvalidArgument, "settings UI root has no type")
	}
	return nil
}
