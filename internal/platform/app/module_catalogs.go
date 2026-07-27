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

// ModuleCatalog is one collection a module exposes, tagged with the module id so
// a caller can list its items and materialise from it. The catalog itself is
// virtual — a view the source computes, never persisted (ADR 0028).
type ModuleCatalog struct {
	ModuleID string
	Catalog  v1.Catalog
}

// ListModuleCatalogsQuery lists the collections the enabled modules expose, for
// the admin collection browser.
type ListModuleCatalogsQuery struct {
	Caller v1.Caller
}

// ListModuleCatalogsResult carries every module's catalogs.
type ListModuleCatalogsResult struct {
	Catalogs []ModuleCatalog
}

// ListModuleCatalogs enumerates the catalogs of every registered
// CatalogProvider. A provider that errors is skipped, like the search fan-out.
// It reads only: catalogs are virtual, and nothing here touches the graph.
func (s *Service) ListModuleCatalogs(ctx context.Context, q ListModuleCatalogsQuery) (ListModuleCatalogsResult, error) {
	if q.Caller.Session == "" {
		return ListModuleCatalogsResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return ListModuleCatalogsResult{}, err
	}
	if s.capabilities == nil {
		return ListModuleCatalogsResult{}, nil
	}

	// Fan out to the catalog providers concurrently, same shape as the search
	// fan-out: settings-read failure aborts, a downed provider is skipped. The
	// fallback tier is consulted only when the ordinary providers between them
	// offered no catalog at all — a home screen should show one source's rows,
	// not the same films twice under two names (see RegisterFallback).
	catalogs, err := fanOutPreferred(ctx, s.capabilities.CatalogProviders(),
		func(e CatalogProviderEntry) bool { return e.Fallback },
		func(ctx context.Context, e CatalogProviderEntry) ([]ModuleCatalog, error) {
			settings, err := s.readModuleSettings(ctx, e.ModuleID)
			if err != nil {
				return nil, err
			}
			resp, err := e.Provider.Catalogs(ctx, v1.CatalogsRequest{Caller: q.Caller, Settings: settings})
			if err != nil {
				return nil, nil
			}
			out := make([]ModuleCatalog, 0, len(resp.Catalogs))
			for _, cat := range resp.Catalogs {
				out = append(out, ModuleCatalog{ModuleID: e.ModuleID, Catalog: cat})
			}
			return out, nil
		})
	if err != nil {
		return ListModuleCatalogsResult{}, err
	}
	return ListModuleCatalogsResult{Catalogs: catalogs}, nil
}

// ListCatalogItemsQuery pages one module catalog's items, addressed by the
// module and the catalog's native id and type.
type ListCatalogItemsQuery struct {
	Caller     v1.Caller
	ModuleID   string
	CatalogID  string
	NativeType string
	Skip       int
}

// ListCatalogItemsResult carries one page of virtual items, each marked
// in-library or not.
type ListCatalogItemsResult struct {
	Items []v1.CatalogItem
	// HasMore is the provider's statement that another page exists (SDK
	// v0.23.0). It is carried through rather than inferred: a full page is not
	// evidence of another, and a provider that says nothing is saying this is
	// the last one — which is what every provider said before the field existed.
	HasMore bool
}

// ListCatalogItems lists a module catalog's entries as virtual candidates an
// admin can select to publish (ADR 0028), marking each one in-library. It reads
// only; materialising a selection is a separate command.
func (s *Service) ListCatalogItems(ctx context.Context, q ListCatalogItemsQuery) (ListCatalogItemsResult, error) {
	if q.Caller.Session == "" {
		return ListCatalogItemsResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.ModuleID == "" || q.CatalogID == "" {
		return ListCatalogItemsResult{}, contracts.NewError(contracts.InvalidArgument, "module id and catalog id are required")
	}
	az, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"})
	if err != nil {
		return ListCatalogItemsResult{}, err
	}
	return s.catalogItemsPage(ctx, az, q)
}

// catalogItemsPage is ListCatalogItems with the boundary already cleared.
//
// It exists because a library rule pages a catalog several times in one
// evaluation (ADR 0104), and calling the entry point per page would
// re-authenticate and re-authorise for every page of one rule of one run —
// the exact shape that made a ten-result search cost ten boundary cycles.
// Requiring an authorized is what says "already inside" in a signature rather
// than in a comment (ADR 0066).
func (s *Service) catalogItemsPage(ctx context.Context, az authorized, q ListCatalogItemsQuery) (ListCatalogItemsResult, error) {
	provider, ok := s.capabilityCatalogProvider(q.ModuleID)
	if !ok {
		return ListCatalogItemsResult{}, contracts.NewError(contracts.NotFound, "no catalog provider registered under id "+q.ModuleID)
	}
	settings, err := s.readModuleSettings(ctx, q.ModuleID)
	if err != nil {
		return ListCatalogItemsResult{}, err
	}
	resp, err := provider.CatalogItems(ctx, v1.CatalogItemsRequest{
		Caller: q.Caller, Settings: settings, CatalogID: q.CatalogID, NativeType: q.NativeType, Skip: q.Skip,
	})
	if err != nil {
		return ListCatalogItemsResult{}, contracts.WrapError(contracts.Unavailable, "list catalog items", err)
	}
	// The same per-item dedup as search, and it had the same defect: a catalog
	// page is the home screen, so this loop is the first thing a signed-in user
	// pays for. It is not in the reported issue — the type change found it.
	items := resp.Items
	for i := range items {
		items[i].InLibrary, items[i].NodeID = s.resolveInLibrary(ctx, az, items[i].Ref)
	}
	return ListCatalogItemsResult{Items: items, HasMore: resp.HasMore}, nil
}

// capabilityCatalogProvider resolves a catalog provider by module id, tolerating
// a Service built without a registry.
func (s *Service) capabilityCatalogProvider(id string) (v1.CatalogProvider, bool) {
	if s.capabilities == nil {
		return nil, false
	}
	return s.capabilities.CatalogProvider(id)
}
