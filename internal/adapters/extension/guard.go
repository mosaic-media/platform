// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The two halves of the handle mechanism (platform#39).
//
// guardedCapability wraps the module proxy on the way out: every method mints a
// handle for the real Caller, hands the module the handle instead, and revokes
// it when the call returns.
//
// resolvingContent wraps the Platform's ContentService on the way back: every
// method exchanges the handle the module presented for the Caller it was minted
// for, and refuses anything not currently live.
//
// Both are written out method by method rather than generated or reflected.
// That is a lot of near-identical code, and it is the trade taken here: these are
// the two places where a module's authority is decided, and a reader can see that
// every path mints and every path resolves without trusting a helper to have
// covered them all. A missing method in a reflective version would be an
// invisible authority leak; a missing method here does not compile, because the
// interface assertions below name every provider interface.

// ─── Outbound: mint a handle per invocation ─────────────────────────────────

type guardedCapability struct {
	inner v1.Capability
	inv   *invocations
}

var (
	_ v1.Capability         = (*guardedCapability)(nil)
	_ v1.MetadataProvider   = (*guardedCapability)(nil)
	_ v1.SearchProvider     = (*guardedCapability)(nil)
	_ v1.CatalogProvider    = (*guardedCapability)(nil)
	_ v1.StreamProvider     = (*guardedCapability)(nil)
	_ v1.SubtitlesProvider  = (*guardedCapability)(nil)
	_ v1.ArtworkProvider    = (*guardedCapability)(nil)
	_ v1.PlaybackProvider   = (*guardedCapability)(nil)
	_ v1.SettingsUIProvider = (*guardedCapability)(nil)
)

func (g *guardedCapability) Manifest() v1.Manifest { return g.inner.Manifest() }

// Import is the only method that can call back, but every method below mints
// anyway. A read role that never writes still receives a Caller, and handing it
// a real session reference "because it cannot use it" would make the guarantee
// depend on the module's behaviour rather than on the Platform's.
func (g *guardedCapability) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.Import(ctx, svc, req)
}

func (g *guardedCapability) Metadata(ctx context.Context, req v1.MetadataRequest) (v1.ContentMetadata, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.MetadataProvider).Metadata(ctx, req)
}

func (g *guardedCapability) Search(ctx context.Context, req v1.SearchRequest) (v1.SearchResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.SearchProvider).Search(ctx, req)
}

func (g *guardedCapability) Catalogs(ctx context.Context, req v1.CatalogsRequest) (v1.CatalogsResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.CatalogProvider).Catalogs(ctx, req)
}

func (g *guardedCapability) CatalogItems(ctx context.Context, req v1.CatalogItemsRequest) (v1.CatalogItemsResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.CatalogProvider).CatalogItems(ctx, req)
}

func (g *guardedCapability) Streams(ctx context.Context, req v1.StreamRequest) (v1.StreamResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.StreamProvider).Streams(ctx, req)
}

func (g *guardedCapability) Subtitles(ctx context.Context, req v1.SubtitlesRequest) (v1.SubtitlesResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.SubtitlesProvider).Subtitles(ctx, req)
}

func (g *guardedCapability) Artwork(ctx context.Context, req v1.ArtworkRequest) (v1.ArtworkResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.ArtworkProvider).Artwork(ctx, req)
}

func (g *guardedCapability) Resolve(ctx context.Context, req v1.PlaybackRequest) (v1.PlaybackResolution, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.PlaybackProvider).Resolve(ctx, req)
}

func (g *guardedCapability) SettingsUI(ctx context.Context, req v1.SettingsUIRequest) (v1.SettingsUIResponse, error) {
	handle, revoke := g.inv.mint(req.Caller)
	defer revoke()
	req.Caller = v1.CallerFromSession(handle)
	return g.inner.(v1.SettingsUIProvider).SettingsUI(ctx, req)
}

// ─── Inbound: resolve the handle, or refuse ─────────────────────────────────

type resolvingContent struct {
	inner v1.ContentService
	inv   *invocations
}

var _ v1.ContentService = (*resolvingContent)(nil)

func (r *resolvingContent) AddContentWork(ctx context.Context, cmd v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.AddContentWorkResult{}, err
	}
	cmd.Caller = caller
	return r.inner.AddContentWork(ctx, cmd)
}

func (r *resolvingContent) AddContentChild(ctx context.Context, cmd v1.AddContentChildCommand) (v1.AddContentChildResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.AddContentChildResult{}, err
	}
	cmd.Caller = caller
	return r.inner.AddContentChild(ctx, cmd)
}

func (r *resolvingContent) AttachContentPart(ctx context.Context, cmd v1.AttachContentPartCommand) (v1.AttachContentPartResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.AttachContentPartResult{}, err
	}
	cmd.Caller = caller
	return r.inner.AttachContentPart(ctx, cmd)
}

func (r *resolvingContent) SetContentArtwork(ctx context.Context, cmd v1.SetContentArtworkCommand) (v1.SetContentArtworkResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.SetContentArtworkResult{}, err
	}
	cmd.Caller = caller
	return r.inner.SetContentArtwork(ctx, cmd)
}

func (r *resolvingContent) RelateContent(ctx context.Context, cmd v1.RelateContentCommand) (v1.RelateContentResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.RelateContentResult{}, err
	}
	cmd.Caller = caller
	return r.inner.RelateContent(ctx, cmd)
}

func (r *resolvingContent) BindContentSource(ctx context.Context, cmd v1.BindContentSourceCommand) (v1.BindContentSourceResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.BindContentSourceResult{}, err
	}
	cmd.Caller = caller
	return r.inner.BindContentSource(ctx, cmd)
}

func (r *resolvingContent) ResolveContentBinding(ctx context.Context, cmd v1.ResolveContentBindingCommand) (v1.ResolveContentBindingResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.ResolveContentBindingResult{}, err
	}
	cmd.Caller = caller
	return r.inner.ResolveContentBinding(ctx, cmd)
}

func (r *resolvingContent) SearchContent(ctx context.Context, q v1.SearchContentQuery) (v1.SearchContentResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.SearchContentResult{}, err
	}
	q.Caller = caller
	return r.inner.SearchContent(ctx, q)
}

func (r *resolvingContent) FindContentByExternalID(ctx context.Context, q v1.FindContentByExternalIDQuery) (v1.FindContentByExternalIDResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.FindContentByExternalIDResult{}, err
	}
	q.Caller = caller
	return r.inner.FindContentByExternalID(ctx, q)
}

func (r *resolvingContent) GetContentNode(ctx context.Context, q v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.GetContentNodeResult{}, err
	}
	q.Caller = caller
	return r.inner.GetContentNode(ctx, q)
}

func (r *resolvingContent) ListContentParts(ctx context.Context, q v1.ListContentPartsQuery) (v1.ListContentPartsResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.ListContentPartsResult{}, err
	}
	q.Caller = caller
	return r.inner.ListContentParts(ctx, q)
}

func (r *resolvingContent) RecordPlaybackProgress(ctx context.Context, cmd v1.RecordPlaybackProgressCommand) (v1.RecordPlaybackProgressResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.RecordPlaybackProgressResult{}, err
	}
	cmd.Caller = caller
	return r.inner.RecordPlaybackProgress(ctx, cmd)
}

func (r *resolvingContent) SetPlaybackFinished(ctx context.Context, cmd v1.SetPlaybackFinishedCommand) (v1.SetPlaybackFinishedResult, error) {
	caller, err := r.inv.resolve(cmd.Caller.Session)
	if err != nil {
		return v1.SetPlaybackFinishedResult{}, err
	}
	cmd.Caller = caller
	return r.inner.SetPlaybackFinished(ctx, cmd)
}

func (r *resolvingContent) GetPlaybackState(ctx context.Context, q v1.GetPlaybackStateQuery) (v1.GetPlaybackStateResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.GetPlaybackStateResult{}, err
	}
	q.Caller = caller
	return r.inner.GetPlaybackState(ctx, q)
}

func (r *resolvingContent) ListPlaybackStates(ctx context.Context, q v1.ListPlaybackStatesQuery) (v1.ListPlaybackStatesResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.ListPlaybackStatesResult{}, err
	}
	q.Caller = caller
	return r.inner.ListPlaybackStates(ctx, q)
}

func (r *resolvingContent) ListInProgress(ctx context.Context, q v1.ListInProgressQuery) (v1.ListInProgressResult, error) {
	caller, err := r.inv.resolve(q.Caller.Session)
	if err != nil {
		return v1.ListInProgressResult{}, err
	}
	q.Caller = caller
	return r.inner.ListInProgress(ctx, q)
}
