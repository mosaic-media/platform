// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
)

// GetPendingConfigVersionQuery reads the configuration version somebody asked
// for that is waiting for an escalation to apply it.
type GetPendingConfigVersionQuery struct {
	CallerSessionID domain.SessionID
}

// GetPendingConfigVersionResult is what is waiting, and what would apply it.
//
// Found is separate from Version because "nothing is waiting" is the ordinary
// state and not a NotFound error: a screen asking what is pending expects the
// usual answer to be "nothing", and making that an error would have every
// render of the panel produce one.
//
// ReloadClass is **re-derived from the diff against whatever is Active now**,
// not read back off the record, for the reason Manager.ApplyPending re-derives
// it: the class is a function of two payloads, and an activation between the
// request and this read can change it. Storing it at request time would persist
// a value a later activation could quietly invalidate.
type GetPendingConfigVersionResult struct {
	Version     domain.ConfigVersion
	Found       bool
	ReloadClass config.ReloadClass
}

func validateGetPendingConfigVersionQuery(query GetPendingConfigVersionQuery) error {
	if query.CallerSessionID == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller session id is required")
	}
	return nil
}

// GetPendingConfigVersion implements the query order, reading through the
// direct (non-transactional) ConfigStore handle.
//
// It authorises ActionConfigRead — the same grant that reads the Active version
// — because it discloses the same thing: what this install is configured to do.
func (s *Service) GetPendingConfigVersion(ctx context.Context, query GetPendingConfigVersionQuery) (GetPendingConfigVersionResult, error) {
	if err := validateGetPendingConfigVersionQuery(query); err != nil {
		return GetPendingConfigVersionResult{}, err
	}

	if _, err := s.enterSession(ctx, query.CallerSessionID, ActionConfigRead,
		policy.Resource{Type: "config"}); err != nil {
		return GetPendingConfigVersionResult{}, err
	}

	pending, err := s.configStore.FindPending(ctx)
	switch {
	case err == nil:
	case contracts.CategoryOf(err) == contracts.NotFound:
		return GetPendingConfigVersionResult{}, nil
	default:
		return GetPendingConfigVersionResult{}, err
	}

	// The payload to diff against. A fresh install has no Active version, and
	// the empty payload is the right comparand there rather than a reason to
	// fail: every field in the pending version is then a change, which is
	// exactly what it is.
	var current domain.ConfigVersion
	active, err := s.configStore.FindActive(ctx)
	switch {
	case err == nil:
		current = active
	case contracts.CategoryOf(err) == contracts.NotFound:
	default:
		return GetPendingConfigVersionResult{}, err
	}

	return GetPendingConfigVersionResult{
		Version:     pending,
		Found:       true,
		ReloadClass: config.RequiredClassBetween(current.Payload, pending.Payload),
	}, nil
}
