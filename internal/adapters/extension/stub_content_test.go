// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package extension_test

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// stubContentService satisfies v1.ContentService with zero values so a test can
// embed it and override only what it observes. Nothing here is exercised except
// through an override.
type stubContentService struct{}

var _ v1.ContentService = (*stubContentService)(nil)

func (stubContentService) AddContentWork(context.Context, v1.AddContentWorkCommand) (v1.AddContentWorkResult, error) {
	return v1.AddContentWorkResult{}, nil
}
func (stubContentService) AddContentChild(context.Context, v1.AddContentChildCommand) (v1.AddContentChildResult, error) {
	return v1.AddContentChildResult{}, nil
}
func (stubContentService) AttachContentPart(context.Context, v1.AttachContentPartCommand) (v1.AttachContentPartResult, error) {
	return v1.AttachContentPartResult{}, nil
}
func (stubContentService) SetContentArtwork(context.Context, v1.SetContentArtworkCommand) (v1.SetContentArtworkResult, error) {
	return v1.SetContentArtworkResult{}, nil
}
func (stubContentService) RelateContent(context.Context, v1.RelateContentCommand) (v1.RelateContentResult, error) {
	return v1.RelateContentResult{}, nil
}
func (stubContentService) BindContentSource(context.Context, v1.BindContentSourceCommand) (v1.BindContentSourceResult, error) {
	return v1.BindContentSourceResult{}, nil
}
func (stubContentService) ResolveContentBinding(context.Context, v1.ResolveContentBindingCommand) (v1.ResolveContentBindingResult, error) {
	return v1.ResolveContentBindingResult{}, nil
}
func (stubContentService) SearchContent(context.Context, v1.SearchContentQuery) (v1.SearchContentResult, error) {
	return v1.SearchContentResult{}, nil
}
func (stubContentService) FindContentByExternalID(context.Context, v1.FindContentByExternalIDQuery) (v1.FindContentByExternalIDResult, error) {
	return v1.FindContentByExternalIDResult{}, nil
}
func (stubContentService) GetContentNode(context.Context, v1.GetContentNodeQuery) (v1.GetContentNodeResult, error) {
	return v1.GetContentNodeResult{}, nil
}
func (stubContentService) ListContentParts(context.Context, v1.ListContentPartsQuery) (v1.ListContentPartsResult, error) {
	return v1.ListContentPartsResult{}, nil
}
func (stubContentService) RecordPlaybackProgress(context.Context, v1.RecordPlaybackProgressCommand) (v1.RecordPlaybackProgressResult, error) {
	return v1.RecordPlaybackProgressResult{}, nil
}
func (stubContentService) SetPlaybackFinished(context.Context, v1.SetPlaybackFinishedCommand) (v1.SetPlaybackFinishedResult, error) {
	return v1.SetPlaybackFinishedResult{}, nil
}
func (stubContentService) GetPlaybackState(context.Context, v1.GetPlaybackStateQuery) (v1.GetPlaybackStateResult, error) {
	return v1.GetPlaybackStateResult{}, nil
}
func (stubContentService) ListPlaybackStates(context.Context, v1.ListPlaybackStatesQuery) (v1.ListPlaybackStatesResult, error) {
	return v1.ListPlaybackStatesResult{}, nil
}
func (stubContentService) ListInProgress(context.Context, v1.ListInProgressQuery) (v1.ListInProgressResult, error) {
	return v1.ListInProgressResult{}, nil
}
