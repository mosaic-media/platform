// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"strconv"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

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
		return nil, err
	}
	if len(res.Catalogs) == 0 {
		return ui.Screen(ui.Title("Collections"),
			ui.EmptyState(emptyIconCollections, "No collections yet — configure a module addon first")).Build(), nil
	}
	rows := make([]ui.El, 0, len(res.Catalogs))
	for _, c := range res.Catalogs {
		rows = append(rows, ui.Button(c.Catalog.Name, "secondary",
			ui.OnTap(ui.Navigate(screenCatalog, map[string]any{
				paramModuleID: c.ModuleID, paramCatalogID: c.Catalog.ID, paramNativeType: c.Catalog.NativeType, paramTitle: c.Catalog.Name,
			}))))
	}
	return ui.Screen(ui.Title("Collections"), ui.Stack("vertical", 8, rows...)).Build(), nil
}

// catalogScreen lists one collection's items as cards an admin can publish. Like
// the search grid, virtual items carry a materialise action and in-library ones
// a badge and a detail navigation.
func (s *Service) catalogScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	moduleID := stringParam(params, paramModuleID)
	catalogID := stringParam(params, paramCatalogID)
	if moduleID == "" || catalogID == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "catalog screen needs moduleId and catalogId params")
	}
	// One page per screen render, deepening as the viewer reaches the end. The
	// provider says whether there is another; the Platform does not guess.
	page := intParam(params, paramPage)
	if page < 0 {
		page = 0
	}
	nativeType := stringParam(params, paramNativeType)
	var items []v1.CatalogItem
	hasMore := false
	for p := 0; p <= page; p++ {
		res, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
			Caller: caller, ModuleID: moduleID, CatalogID: catalogID,
			NativeType: nativeType, Skip: len(items),
		})
		if err != nil {
			return nil, err
		}
		items = append(items, res.Items...)
		hasMore = res.HasMore
		// A provider that returned nothing has nothing further, whatever it says
		// — otherwise a wrong HasMore is an unbounded loop over an empty page.
		if len(res.Items) == 0 || !res.HasMore {
			hasMore = false
			break
		}
	}
	// The catalog's own name, carried in the navigation that opened it. The
	// Platform can address a catalog by id without being able to name it — the
	// provider's list is what holds the name — so a screen reached without one
	// says "Collection" rather than guessing.
	name := stringParam(params, paramTitle)
	if name == "" {
		name = "Collection"
	}

	res := app.ListCatalogItemsResult{Items: items}
	if len(res.Items) == 0 {
		return ui.Screen(ui.Title(name),
			ui.EmptyState(emptyIconCollections, "This collection is empty")).Build(), nil
	}
	cards := make([]ui.El, 0, len(res.Items))
	for _, it := range res.Items {
		cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
	}
	grid := []ui.El{ui.Group(cards...)}
	if hasMore {
		grid = append(grid, ui.HasMore(true), ui.LoadMore(ui.Query(screenCatalog, map[string]any{
			paramModuleID: moduleID, paramCatalogID: catalogID,
			paramNativeType: nativeType, paramTitle: name, paramPage: page + 1,
		})))
	}

	// The spotlight: the catalog's leading item as a wide banner over the grid,
	// in the Screen's full-bleed slot. It is the same enrichment the home hero
	// pays for, so it is only worth it on the first page — paging deeper is a
	// viewer already scanning the grid, and re-fetching the same backdrop to
	// redraw an unchanged banner is a round-trip for nothing.
	els := []ui.El{ui.Title(name), ui.Subtitle(countLabel(len(res.Items), hasMore))}
	if page == 0 {
		if spot := s.spotlightFromItem(ctx, caller, res.Items[0]); spot != nil {
			els = append(els, ui.Slot("bleed", spot))
		}
	}
	els = append(els, ui.Grid(grid...))
	return ui.Screen(els...).Build(), nil
}

// countLabel is the "128 titles" beside a collection's heading. It says "128+"
// when the provider has more to give, because the number rendered is the number
// loaded and claiming it is the total is wrong in the common case.
func countLabel(n int, hasMore bool) string {
	unit := " titles"
	if n == 1 {
		unit = " title"
	}
	if hasMore {
		return strconv.Itoa(n) + "+" + unit
	}
	return strconv.Itoa(n) + unit
}

// spotlightFromItem is the catalog screen's banner: the leading item, enriched
// with the backdrop its card lacks, as a wide hero over the grid. It is the
// `detail` hero variant rather than the home's `feature` — a collection's
// spotlight introduces the row beneath it and must not fill the viewport the
// grid is supposed to occupy.
//
// A failed enrichment yields nil and the screen renders as a plain grid, which
// is what it was before the banner existed.
func (s *Service) spotlightFromItem(ctx context.Context, caller v1.Caller, it v1.CatalogItem) ui.El {
	prev, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: it.Ref})
	if err != nil {
		return nil
	}
	m := prev.Metadata
	title := m.Title
	if title == "" {
		title = it.Title
	}
	if m.Backdrop == "" {
		// No backdrop is no spotlight. A wide banner over a flat surface is a
		// large empty box, which is worse than the grid starting at the top.
		return nil
	}
	return ui.Hero(title,
		ui.Prop("variant", "detail"),
		ui.Kicker("Spotlight"),
		ui.Backdrop(s.art(m.Backdrop)),
		ui.When(m.Logo != "", ui.Logo(s.art(m.Logo))),
		ui.When(m.Overview != "", ui.Overview(m.Overview)),
		ui.Actions(ui.Button("More info", "primary", ui.IconName("info"),
			ui.OnTap(ui.Navigate(screenDetail, map[string]any{paramRef: refInput(it.Ref)})))),
	)
}
