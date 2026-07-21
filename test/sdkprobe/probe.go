// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Package probe exercises every part of the published content surface the way
// a capability would, so that compiling this module is a real test of the
// surface's completeness and self-containment (ADR 0016). It imports only
// contracts/platform/v1 and the standard library.
package probe

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// SourceAndAdd is shaped like the reference capability's core: it acts as its
// invoking caller (ADR 0017), checks whether the work already exists, then
// creates a work, a season, an episode, a part, an adaptation edge and a
// source binding — using only the published service and models.
func SourceAndAdd(ctx context.Context, svc v1.ContentService, caller v1.Caller) error {
	// Do I already have this? — by external id, then by title.
	if _, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: caller, Scheme: "anilist", Value: "5114",
	}); err != nil {
		return err
	}
	if _, err := svc.SearchContent(ctx, v1.SearchContentQuery{
		Caller: caller, Title: "Fullmetal Alchemist", Kind: v1.NodeWork, MediaType: v1.MediaAnimeSeries, Limit: 20,
	}); err != nil {
		return err
	}

	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: caller, MediaType: v1.MediaAnimeSeries, Title: "Fullmetal Alchemist: Brotherhood",
		ExternalIDs: []byte(`{"anilist":"5114"}`),
	})
	if err != nil {
		return err
	}

	season, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
		Caller: caller, ParentID: work.Work.ID,
		Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason, Title: "Season 1", NaturalOrder: 1,
	})
	if err != nil {
		return err
	}

	episode, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
		Caller: caller, ParentID: season.Node.ID,
		Kind: v1.NodeItem, ItemType: v1.ItemEpisode, Title: "Fullmetal Alchemist", NaturalOrder: 1,
	})
	if err != nil {
		return err
	}

	if _, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
		Caller: caller, NodeID: episode.Node.ID, Role: v1.PartEdition,
		Location: v1.MediaLocation{Scheme: v1.LocalLocation, Ref: "/media/fmab/s01e01.mkv"},
	}); err != nil {
		return err
	}

	if _, err := svc.BindContentSource(ctx, v1.BindContentSourceCommand{
		Caller: caller, NodeID: work.Work.ID,
		SourceProvider: "anilist", SourceRef: "5114",
		MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact, Status: v1.BindingConfirmed,
	}); err != nil {
		return err
	}

	// A read-back that also touches the remaining surface.
	if _, err := svc.GetContentNode(ctx, v1.GetContentNodeQuery{
		Caller: caller, NodeID: work.Work.ID, WithChildren: true,
	}); err != nil {
		return err
	}

	return nil
}

// RelateAndResolve exercises the association and identity halves so the whole
// ContentService is referenced, including the enums a capability must be able
// to name.
func RelateAndResolve(ctx context.Context, svc v1.ContentService, caller v1.Caller, anime, manga v1.NodeID, binding v1.SourceBindingID) error {
	if _, err := svc.RelateContent(ctx, v1.RelateContentCommand{
		Caller: caller, FromNodeID: anime, ToNodeID: manga,
		Type: v1.RelationAdaptation, Confidence: 0.98, Origin: v1.OriginProviderSupplied,
	}); err != nil {
		return err
	}
	if _, err := svc.ResolveContentBinding(ctx, v1.ResolveContentBindingCommand{
		Caller: caller, BindingID: binding, Resolution: v1.ResolveConfirm,
	}); err != nil {
		return err
	}
	return nil
}
