package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
)

// ActionConfigRead is the policy action evaluated for
// GetActiveConfigVersion and GetConfigVersion (MEG-015 §09 —
// Configuration: "active version").
const ActionConfigRead policy.Action = "config.read"

// GetActiveConfigVersionQuery reads the currently Active configuration
// version, if any.
type GetActiveConfigVersionQuery struct {
	CallerSessionID domain.SessionID
}

// GetActiveConfigVersionResult is the Platform result type returned by
// GetActiveConfigVersion.
type GetActiveConfigVersionResult struct {
	Version domain.ConfigVersion
}

func validateGetActiveConfigVersionQuery(query GetActiveConfigVersionQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	return nil
}

// GetActiveConfigVersion implements the query boundary from MEG-015 §04,
// reading through the direct (non-transactional) ConfigStore handle.
func (s *Service) GetActiveConfigVersion(ctx context.Context, query GetActiveConfigVersionQuery) (GetActiveConfigVersionResult, error) {
	if err := validateGetActiveConfigVersionQuery(query); err != nil {
		return GetActiveConfigVersionResult{}, err
	}

	callerID, err := s.authenticate(ctx, query.CallerSessionID)
	if err != nil {
		return GetActiveConfigVersionResult{}, err
	}

	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionConfigRead, policy.Resource{Type: "config"}, policy.PolicyContext{}); err != nil {
		return GetActiveConfigVersionResult{}, err
	}

	version, err := s.configStore.FindActive(ctx)
	if err != nil {
		return GetActiveConfigVersionResult{}, err
	}
	return GetActiveConfigVersionResult{Version: version}, nil
}
