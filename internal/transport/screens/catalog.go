// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"

	sdui "github.com/mosaic-media/sdui/sdui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// collectionsScreen is the admin's entry to curation: the collections the
// enabled modules expose, each a row that opens the catalog's items. Browsing is
// a read — nothing is published until an item's materialise action runs (ADR
// 0028).
func (s *Service) collectionsScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	res, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Catalogs) == 0 {
		return emptyScreen("Collections", emptyIconCollections, "No collections yet — configure a module addon first"), nil
	}
	rows := make([]sdui.Node, 0, len(res.Catalogs))
	for _, c := range res.Catalogs {
		rows = append(rows, sdui.Button(c.Catalog.Name, "secondary", sdui.Navigate(screenCatalog, map[string]any{
			paramModuleID: c.ModuleID, paramCatalogID: c.Catalog.ID, paramNativeType: c.Catalog.NativeType,
		})))
	}
	return screen("Collections", sdui.Stack("vertical", 8, sdui.Child(rows...))), nil
}

// catalogScreen lists one collection's items as cards an admin can publish. Like
// the search grid, virtual items carry a materialise action and in-library ones
// a badge and a detail navigation.
func (s *Service) catalogScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, paramModuleID)
	catalogID := stringParam(params, paramCatalogID)
	if moduleID == "" || catalogID == "" {
		return sdui.Node{}, contracts.NewError(contracts.InvalidArgument, "catalog screen needs moduleId and catalogId params")
	}
	res, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
		Caller: caller, ModuleID: moduleID, CatalogID: catalogID, NativeType: stringParam(params, paramNativeType),
	})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(res.Items) == 0 {
		return emptyScreen("Collection", emptyIconCollections, "This collection is empty"), nil
	}
	cards := make([]sdui.Node, 0, len(res.Items))
	for _, it := range res.Items {
		cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
	}
	return gridScreen("Collection", cards...), nil
}
