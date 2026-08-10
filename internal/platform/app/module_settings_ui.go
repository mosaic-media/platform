// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ModuleSettingsUIQuery asks a module for its own settings screen (sdk#4).
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

// ModuleSettingsUI resolves a module's contributed settings screen (sdk#4): a
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
	if err := validateUINode(query.ModuleID, resp.UI); err != nil {
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

// validateUINode confines a module-supplied settings screen to a well-formed,
// correctly namespaced UINode tree before the Platform hosts it (sdk#4,
// contracts#9): the bytes must be a JSON object carrying a non-empty string "type",
// and **every** node in the tree must name a type this module may emit.
//
// This is the one boundary a module's own UI crosses, so it is where the
// namespace rule is enforced. It matters because the vocabulary is open and was
// flat: a module returning a node called `PosterCard` — not *using* the core
// component, but contributing a definition of that name — would replace the core
// one in every client's registry, and two modules both contributing a `StatChip`
// would silently overwrite each other. A map's last writer wins and nothing
// errors.
//
// The whole tree rather than the root, because a hole two levels down is exactly
// the kind that survives being looked at.
func validateUINode(moduleID string, ui []byte) error {
	if len(ui) == 0 {
		return contracts.NewError(contracts.InvalidArgument, "empty settings UI")
	}
	if err := sdui.ValidateModuleID(moduleID); err != nil {
		return contracts.WrapError(contracts.InvalidArgument, "module id", err)
	}
	var node map[string]any
	if err := json.Unmarshal(ui, &node); err != nil {
		return err
	}
	if t, _ := node["type"].(string); t == "" {
		return contracts.NewError(contracts.InvalidArgument, "settings UI root has no type")
	}
	return validateNodeTypes(moduleID, node)
}

// validateNodeTypes walks a decoded UINode tree checking every `type` against
// the namespace rule. It walks children and slots explicitly rather than every
// map it meets: props are an open bag, and a screen param that happens to be
// called "type" is the module's data, not a node.
func validateNodeTypes(moduleID string, node map[string]any) error {
	t, _ := node["type"].(string)
	if err := sdui.ValidateModuleType(moduleID, t); err != nil {
		return contracts.WrapError(contracts.InvalidArgument, "settings UI", err)
	}
	for _, child := range nodeList(node["children"]) {
		if err := validateNodeTypes(moduleID, child); err != nil {
			return err
		}
	}
	slots, _ := node["slots"].(map[string]any)
	for _, v := range slots {
		for _, child := range nodeList(v) {
			if err := validateNodeTypes(moduleID, child); err != nil {
				return err
			}
		}
	}
	return nil
}

// nodeList reads a child list, tolerating the single-node form a slot may carry.
func nodeList(v any) []map[string]any {
	switch x := v.(type) {
	case []any:
		out := make([]map[string]any, 0, len(x))
		for _, e := range x {
			if m, ok := e.(map[string]any); ok {
				out = append(out, m)
			}
		}
		return out
	case map[string]any:
		return []map[string]any{x}
	default:
		return nil
	}
}
