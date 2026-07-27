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

	// The catalog's own declaration — what it says it can be narrowed by — read
	// from the one module that serves it rather than from a fan-out to all of
	// them. It is also what validates nothing: the Platform draws the control
	// the provider described and sends back a value the provider named, and the
	// provider refuses anything else (SDK v0.25.0).
	declared := s.catalogFilters(ctx, caller, moduleID, catalogID, nativeType)
	selected := selectedFilters(declared, params)

	var items []v1.CatalogItem
	hasMore := false
	for p := 0; p <= page; p++ {
		res, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
			Caller: caller, ModuleID: moduleID, CatalogID: catalogID,
			NativeType: nativeType, Skip: len(items), Filters: selected,
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
		// The narrowing travels with the page, for the same reason it does on
		// the library screen: a `query` re-renders the whole window, and a
		// next-page action that dropped the filters would widen the browse
		// mid-scroll with nothing to show for it.
		next := map[string]any{
			paramModuleID: moduleID, paramCatalogID: catalogID,
			paramNativeType: nativeType, paramTitle: name, paramPage: page + 1,
		}
		for _, f := range declared {
			if v := selected[f.Name]; v != "" {
				next[filterParam(f.Name)] = v
			}
		}
		grid = append(grid, ui.HasMore(true), ui.LoadMore(ui.Query(screenCatalog, next)))
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
	for _, row := range catalogFacetRows(declared, selected, moduleID, catalogID, nativeType, name) {
		els = append(els, row)
	}
	els = append(els, ui.Grid(grid...))
	return ui.Screen(els...).Build(), nil
}

// filterParam is the screen param a provider's filter is carried in.
//
// Namespaced, because a filter's name is a *source's* own parameter — TMDB's is
// literally `with_genres` — and the screen's params are a Platform namespace
// holding `page`, `moduleId` and the rest. A source that happened to name a
// filter `page` would otherwise silently take over the paging.
func filterParam(name string) string { return "filter." + name }

// catalogFilters reads the declaration for the catalog being rendered.
//
// A failure yields no filters rather than an error: the grid is the screen's
// job and a facet row is an affordance on top of it, so a provider that cannot
// describe its own catalog costs the control and not the page.
func (s *Service) catalogFilters(ctx context.Context, caller v1.Caller, moduleID, catalogID, nativeType string) []v1.CatalogFilter {
	res, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{
		Caller: caller, ModuleID: moduleID,
	})
	if err != nil {
		return nil
	}
	for _, c := range res.Catalogs {
		if c.Catalog.ID == catalogID && c.Catalog.NativeType == nativeType {
			return c.Catalog.Filters
		}
	}
	return nil
}

// selectedFilters reads the params back into the selection, keeping only values
// the catalog actually declared.
//
// **A value that is not on the declared list is dropped here rather than sent**,
// because a provider is required to *refuse* one — which is right, and which
// would turn a stale link into an error screen instead of a browse. The module's
// refusal is the guarantee; this is the screen not asking a question it can see
// is stale.
func selectedFilters(declared []v1.CatalogFilter, params map[string]any) map[string]string {
	var out map[string]string
	for _, f := range declared {
		value := stringParam(params, filterParam(f.Name))
		if value == "" {
			continue
		}
		for _, o := range f.Options {
			if o.Value == value {
				if out == nil {
					out = map[string]string{}
				}
				out[f.Name] = value
				break
			}
		}
	}
	return out
}

// maxCatalogFacets is how many chips one filter row offers.
//
// A source's genre list is a couple of dozen and its streaming-service list can
// be a hundred. The declaration is ordered by the provider — TMDB sorts services
// by its own regional display priority — so the cut takes the tail the source
// itself ranked last.
const maxCatalogFacets = 14

// catalogFacetRows draws one row per declared filter.
func catalogFacetRows(declared []v1.CatalogFilter, selected map[string]string, moduleID, catalogID, nativeType, name string) []ui.El {
	rows := make([]ui.El, 0, len(declared))
	for _, f := range declared {
		if len(f.Options) == 0 {
			continue
		}
		base := func() map[string]any {
			p := map[string]any{
				paramModuleID: moduleID, paramCatalogID: catalogID,
				paramNativeType: nativeType, paramTitle: name,
			}
			// Every *other* filter's selection is preserved, so two rows compose
			// rather than each clearing the other.
			for _, other := range declared {
				if other.Name != f.Name {
					if v := selected[other.Name]; v != "" {
						p[filterParam(other.Name)] = v
					}
				}
			}
			return p
		}

		chips := make([]ui.El, 0, maxCatalogFacets+1)
		if selected[f.Name] != "" {
			chips = append(chips, ui.FilterChip("All", false,
				ui.OnTap(ui.Query(screenCatalog, base()))))
		}
		for i, o := range f.Options {
			if i >= maxCatalogFacets && o.Value != selected[f.Name] {
				continue
			}
			params := base()
			if o.Value != selected[f.Name] {
				params[filterParam(f.Name)] = o.Value
			}
			// When it *is* the selection, the param is simply absent, so pressing
			// the lit chip clears it — the control is reversible by the press
			// that set it rather than needing somewhere separate to go.
			chips = append(chips, ui.FilterChip(o.Label, o.Value == selected[f.Name],
				ui.OnTap(ui.Query(screenCatalog, params))))
		}
		rows = append(rows, ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(chips...)))
	}
	return rows
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
