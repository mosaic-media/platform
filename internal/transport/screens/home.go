// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"

	sdui "github.com/mosaic-media/mosaic-sdui/sdui"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

const (
	homeMaxRows     = 6
	homeMaxRowItems = 20
)

// homeScreen is the default landing surface: a hero over rows of the enabled
// modules' catalogs (Cinemeta's Popular Movies/Series, etc. — ADR 0028's virtual
// plane, browsed not materialised). Each row is a carousel of cards that open a
// detail; the hero is the first catalog's first item, enriched with its backdrop
// and logo. Browsing is a read, so nothing here writes.
func (s *Service) homeScreen(ctx context.Context, caller v1.Caller) (sdui.Node, error) {
	cats, err := s.content.ListModuleCatalogs(ctx, app.ListModuleCatalogsQuery{Caller: caller})
	if err != nil {
		return sdui.Node{}, err
	}
	if len(cats.Catalogs) == 0 {
		return emptyScreen("Home", emptyIconCollections, "Nothing here yet — add an addon in Settings to browse content"), nil
	}

	body := make([]sdui.Node, 0, homeMaxRows+1)
	heroAdded := false
	rows := 0
	for _, c := range cats.Catalogs {
		if rows >= homeMaxRows {
			break
		}
		items, err := s.content.ListCatalogItems(ctx, app.ListCatalogItemsQuery{
			Caller: caller, ModuleID: c.ModuleID, CatalogID: c.Catalog.ID, NativeType: c.Catalog.NativeType,
		})
		if err != nil || len(items.Items) == 0 {
			continue
		}
		if !heroAdded {
			if hero, ok := s.heroFromItem(ctx, caller, items.Items[0]); ok {
				body = append(body, hero)
				heroAdded = true
			}
		}
		cards := make([]sdui.Node, 0, homeMaxRowItems)
		for i, it := range items.Items {
			if i >= homeMaxRowItems {
				break
			}
			cards = append(cards, s.contentCard(it.Ref, it.Title, it.Year, it.Poster, it.InLibrary))
		}
		body = append(body, carouselSection(c.Catalog.Name, cards...))
		rows++
	}
	if len(body) == 0 {
		return emptyScreen("Home", emptyIconCollections, "Nothing to show yet — try adding an addon in Settings"), nil
	}
	return screen("Home", body...), nil
}

// heroFromItem builds a home hero from a catalog item, enriching it with the
// backdrop, logo and overview its lightweight card lacks (ADR 0034). A metadata
// fetch that fails just yields no hero rather than failing the home screen.
func (s *Service) heroFromItem(ctx context.Context, caller v1.Caller, it v1.CatalogItem) (sdui.Node, bool) {
	prev, err := s.content.PreviewContent(ctx, app.PreviewContentQuery{Caller: caller, Ref: it.Ref})
	if err != nil {
		return sdui.Node{}, false
	}
	m := prev.Metadata
	title := m.Title
	if title == "" {
		title = it.Title
	}
	opts := []sdui.Option{
		sdui.Backdrop(s.art(m.Backdrop)),
		sdui.Slot("actions", sdui.Button("View", "primary",
			sdui.Navigate(screenDetail, map[string]any{paramRef: refInput(it.Ref)}))),
	}
	if m.Logo != "" {
		opts = append(opts, sdui.Logo(s.art(m.Logo)))
	}
	if m.Overview != "" {
		opts = append(opts, sdui.Overview(m.Overview))
	}
	var pills []string
	if y := yearLabel(m.Year); y != "" {
		pills = append(pills, y)
	}
	if m.Rating > 0 {
		pills = append(pills, fmt.Sprintf("★ %.1f", m.Rating))
	}
	if len(pills) > 0 {
		opts = append(opts, sdui.Meta(pills...))
	}
	return sdui.HeroBanner(title, opts...), true
}
