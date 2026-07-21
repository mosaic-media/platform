// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// GetConfigVersionQuery reads a single configuration version by ID, so a
// caller can check the outcome of a prior draft/validate/activate command.
type GetConfigVersionQuery struct {
	CallerSessionID domain.SessionID
	ConfigVersionID domain.ConfigVersionID
}

// GetConfigVersionResult is the Platform result type returned by
// GetConfigVersion.
type GetConfigVersionResult struct {
	Version domain.ConfigVersion
}

func validateGetConfigVersionQuery(query GetConfigVersionQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	if query.ConfigVersionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "config version id is required")
	}
	return nil
}

// GetConfigVersion implements the query boundary, per the command order.
func (s *Service) GetConfigVersion(ctx context.Context, query GetConfigVersionQuery) (GetConfigVersionResult, error) {
	if err := validateGetConfigVersionQuery(query); err != nil {
		return GetConfigVersionResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return GetConfigVersionResult{}, err
	}

	resource := policy.Resource{Type: "config", ID: string(query.ConfigVersionID)}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionConfigRead, resource, policy.PolicyContext{}); err != nil {
		return GetConfigVersionResult{}, err
	}

	version, err := s.configStore.FindByID(ctx, query.ConfigVersionID)
	if err != nil {
		return GetConfigVersionResult{}, err
	}
	return GetConfigVersionResult{Version: version}, nil
}
