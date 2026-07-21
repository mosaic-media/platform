// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"

	"google.golang.org/protobuf/encoding/protojson"

	sdui "github.com/mosaic-media/sdui/sdui"
	"github.com/mosaic-media/sdui/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
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
		return nil, err
	}
	// The module returns its settings UI as a UINode; decode it into the typed
	// node (protojson, since the tree is now protobuf — ADR 0044).
	node := ui.Component("").Build()
	if err := protojson.Unmarshal(res.UI, node); err != nil {
		return nil, contracts.WrapError(contracts.Internal, "decode module settings UI", err)
	}
	return node, nil
}
