// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"encoding/json"

	sdui "github.com/mosaic-media/mosaic-sdui/sdui"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// settingsScreen hosts a module's own contributed settings UI (ADR 0038). The
// Platform owns the frame; the module fills it — the settings screen renders the
// UINode tree the module returned through ModuleSettingsUI, validated by the app
// service. It takes a moduleId param, defaulting to the Stremio module (the only
// one that provides a settings UI today); a settings index over several modules
// is a later addition.
func (s *Service) settingsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, paramModuleID)
	if moduleID == "" {
		moduleID = defaultSettingsModule
	}
	res, err := s.content.ModuleSettingsUI(ctx, app.ModuleSettingsUIQuery{Caller: caller, ModuleID: moduleID})
	if err != nil {
		return sdui.Node{}, err
	}
	var node sdui.Node
	if err := json.Unmarshal(res.UI, &node); err != nil {
		return sdui.Node{}, contracts.WrapError(contracts.Internal, "decode module settings UI", err)
	}
	return node, nil
}
