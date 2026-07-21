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

// PreviewContentQuery resolves the detail of a virtual content item — one a
// search or catalog browse produced but that is not (yet) in the library. It is
// what lets a user open a virtual result to see more before adding it (ADR
// 0028): reading is free, materialising is the deliberate act.
type PreviewContentQuery struct {
	Caller v1.Caller
	Ref    v1.ContentRef
}

// PreviewContentResult carries the previewed metadata, plus whether the ref
// already resolves to a library Work (and that Work's node id). The metadata is
// filled for a virtual and an in-library ref alike (ADR 0034), so one ref-based
// detail builder serves both planes; InLibrary only switches the primary action.
type PreviewContentResult struct {
	Metadata  v1.ContentMetadata
	InLibrary bool
	NodeID    v1.NodeID
}

// PreviewContent reads a ref's descriptive metadata through the MetadataProvider
// the ref names (ADR 0027's RoleMetadata, used here for a read rather than a
// materialise), and reports whether the ref already resolves to a library Work.
// It resolves both regardless of plane (ADR 0034): a detail screen renders from
// the metadata whether the item is virtual or in-library, and reads InLibrary
// only to choose between an Add-to-library action and an in-library marker. A
// library item's detail is therefore re-derived live from the provider rather
// than read from stored fields — as current as the source, at the cost of
// needing a reachable metadata addon. Nothing here writes.
func (s *Service) PreviewContent(ctx context.Context, q PreviewContentQuery) (PreviewContentResult, error) {
	if q.Caller.Session == "" {
		return PreviewContentResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.Ref.Provider == "" || q.Ref.NativeID == "" || q.Ref.NativeType == "" {
		return PreviewContentResult{}, contracts.NewError(contracts.InvalidArgument, "ref needs a provider, native id and type")
	}

	callerID, err := s.authenticateCaller(ctx, q.Caller)
	if err != nil {
		return PreviewContentResult{}, err
	}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentRead, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return PreviewContentResult{}, err
	}

	// Resolve the library plane, but do not short-circuit: an in-library ref
	// still gets its metadata below, so its detail is as rich as a virtual one
	// (ADR 0034). InLibrary only changes the primary action the caller renders.
	inLib, nodeID := s.resolveInLibrary(ctx, q.Caller, q.Ref)

	provider, ok := s.capabilityMetadataProvider(q.Ref.Provider)
	if !ok {
		return PreviewContentResult{}, contracts.NewError(contracts.NotFound, "no metadata provider registered under id "+q.Ref.Provider)
	}
	settings, err := s.readModuleSettings(ctx, q.Ref.Provider)
	if err != nil {
		return PreviewContentResult{}, err
	}
	meta, err := provider.Metadata(ctx, v1.MetadataRequest{Caller: q.Caller, Settings: settings, Ref: q.Ref})
	if err != nil {
		return PreviewContentResult{}, contracts.WrapError(contracts.Unavailable, "preview content", err)
	}
	return PreviewContentResult{Metadata: meta, InLibrary: inLib, NodeID: nodeID}, nil
}

// capabilityMetadataProvider resolves a metadata provider by module id,
// tolerating a Service built without a registry.
func (s *Service) capabilityMetadataProvider(id string) (v1.MetadataProvider, bool) {
	if s.capabilities == nil {
		return nil, false
	}
	return s.capabilities.MetadataProvider(id)
}
