// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package screens is the Platform's SDUI emit-side (ADR 0029). It builds UINode
// trees from the application query services using the mosaic-sdui Go producer
// binding, and serves them through a name-keyed registry. It is a projection
// surface, exactly like the GraphQL resolvers: every builder calls application
// query services and nothing else — no store, no transaction.
//
// Screens are Platform-emitted. A module contributes content through its
// provider roles (ADR 0027); the Platform owns how that content is shown. A
// screen's Action names a Platform GraphQL operation, so the emitted tree and
// the data its actions drive share one transport.
//
// The package is split one screen to a file — home.go, search.go, catalog.go,
// detail.go, shell.go, settings.go — over the shared card builder (card.go) and
// the small DOM/param helpers (build.go). This file holds the Service, the
// name→builder dispatch, and the vocabulary the builders and their Navigate
// actions agree on.
package screens

import (
	"context"

	sdui "github.com/mosaic-media/sdui/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Screen names. A screen is reached both through Render (the dispatch below) and
// through a Navigate action another screen emits; naming them once keeps the two
// sides from drifting.
const (
	screenShell       = "shell"
	screenHome        = "home"
	screenSearch      = "search"
	screenCollections = "collections"
	screenCatalog     = "catalog"
	screenDetail      = "detail"
	screenSettings    = "settings"
)

// Screen param keys. Each is written into a Navigate action's params by the
// screen that links onward and read back by stringParam in the screen it opens;
// a shared constant keeps the write and the read spelling the same key.
const (
	paramModuleID   = "moduleId"
	paramCatalogID  = "catalogId"
	paramNativeType = "nativeType"
	paramRef        = "ref"
	paramNodeID     = "nodeId"
	paramSeason     = "season"
	paramText       = "text"
)

// Empty-state illustration keys the client maps to an icon.
const (
	emptyIconCollections = "collections"
	emptyIconSearch      = "search"
)

// defaultSettingsModule is the module the settings screen hosts when no moduleId
// is given — Stremio, the only module that provides a settings UI today.
const defaultSettingsModule = "stremio"

// importContentMutation is the Platform mutation a detail's Add-to-library action
// invokes to materialise a virtual ref (ADR 0028).
const importContentMutation = "importContent"

// contentQueries is the slice of the application query surface the screen
// builders read. Narrowing to an interface keeps the emit-side a projection of
// the services (like a GraphQL resolver) and makes the builders testable without
// standing up a full Service. *app.Service satisfies it.
type contentQueries interface {
	SearchAvailableContent(context.Context, app.SearchAvailableContentQuery) (app.SearchAvailableContentResult, error)
	ListModuleCatalogs(context.Context, app.ListModuleCatalogsQuery) (app.ListModuleCatalogsResult, error)
	ListCatalogItems(context.Context, app.ListCatalogItemsQuery) (app.ListCatalogItemsResult, error)
	GetContentNode(context.Context, v1.GetContentNodeQuery) (v1.GetContentNodeResult, error)
	PreviewContent(context.Context, app.PreviewContentQuery) (app.PreviewContentResult, error)
	ModuleSettingsUI(context.Context, app.ModuleSettingsUIQuery) (app.ModuleSettingsUIResult, error)
}

// Service renders named screens. It holds the query surface the builders read
// from, and an artwork rewriter that routes remote poster/backdrop URLs through
// the Platform's artwork proxy (ADR 0030); it opens nothing of its own.
type Service struct {
	content contentQueries
	artwork func(string) string
}

// NewService wires the emit-side to the application services. artwork rewrites a
// remote image URL to a Platform-proxied one; a nil rewriter passes URLs
// through unchanged (a Service built without the proxy).
func NewService(a *app.Service, artwork func(string) string) *Service {
	if artwork == nil {
		artwork = func(u string) string { return u }
	}
	return &Service{content: a, artwork: artwork}
}

// art proxies a non-empty image URL through the artwork rewriter (ADR 0030),
// passing an empty URL and a Service built without a rewriter through unchanged.
func (s *Service) art(u string) string {
	if u == "" || s.artwork == nil {
		return u
	}
	return s.artwork(u)
}

// Render builds the named screen for the caller, with the given params. An
// unknown name is NotFound. The returned Node is the root the client renders.
func (s *Service) Render(ctx context.Context, name string, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	switch name {
	case screenShell:
		return s.shellScreen()
	case screenHome:
		return s.homeScreen(ctx, caller)
	case screenSearch:
		return s.searchScreen(ctx, caller, params)
	case screenCollections:
		return s.collectionsScreen(ctx, caller)
	case screenCatalog:
		return s.catalogScreen(ctx, caller, params)
	case screenDetail:
		return s.detailScreen(ctx, caller, params)
	case screenSettings:
		return s.settingsScreen(ctx, caller, params)
	default:
		return sdui.Node{}, contracts.NewError(contracts.NotFound, "no screen named "+name)
	}
}
