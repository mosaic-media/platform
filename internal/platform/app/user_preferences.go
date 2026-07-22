// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionPreferenceWrite and ActionPreferenceRead are the policy actions for a
// user's own settings.
//
// They exist for the same reason every other action does — the command order
// authorises before it mutates — but they are the weakest authority in the
// Platform, and deliberately so: these commands only ever act on the *caller's
// own* preferences. There is no target-user parameter, so holding the action
// grants a user nothing over anybody else.
//
// Reading or changing another user's preferences is an administrative
// capability that does not exist yet. When it does it needs its own action
// rather than a wider reading of these, because "may configure myself" and
// "may configure anyone" are not the same permission.
const (
	ActionPreferenceWrite policy.Action = "preference.write"
	ActionPreferenceRead  policy.Action = "preference.read"
)

// SetUserPreferenceCommand sets one of the caller's own preferences.
type SetUserPreferenceCommand struct {
	Caller v1.Caller
	// Key is the dotted preference name, e.g. domain.PreferenceExpertMode.
	Key string
	// Value is the preference as JSON. It is stored uninterpreted; the surface
	// that reads it owns its meaning.
	Value []byte
}

// SetUserPreferenceResult is the stored preference.
type SetUserPreferenceResult struct {
	Preference domain.UserPreference
}

// SetUserPreference stores one preference for the calling user.
func (s *Service) SetUserPreference(ctx context.Context, cmd SetUserPreferenceCommand) (SetUserPreferenceResult, error) {
	// 1. validate command shape.
	if cmd.Key == "" {
		return SetUserPreferenceResult{}, contracts.NewError(contracts.InvalidArgument, "preference key is required")
	}
	if len(cmd.Value) > 0 && !json.Valid(cmd.Value) {
		// Uninterpreted is not the same as unvalidated-as-JSON. The column is
		// jsonb, so malformed input would fail at the driver with a message
		// about the database rather than about the request.
		return SetUserPreferenceResult{}, contracts.NewError(contracts.InvalidArgument, "preference value must be valid JSON")
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return SetUserPreferenceResult{}, err
	}

	// 3. authorize the action.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionPreferenceWrite,
		policy.Resource{Type: "preference"}, policy.PolicyContext{}); err != nil {
		return SetUserPreferenceResult{}, err
	}

	pref := domain.UserPreference{
		UserID:    callerID,
		Key:       cmd.Key,
		Value:     cmd.Value,
		UpdatedAt: s.clock.Now(),
	}

	// 4. persist the preference and its outbox event in one transaction.
	var stored domain.UserPreference
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		var err error
		stored, err = tx.UserPreferences().Set(ctx, pref)
		if err != nil {
			return err
		}
		// The key, never the value. A key is a fixed vocabulary this
		// repository writes; a value is whatever a user chose, and an event
		// payload is not the place to start carrying that.
		return tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "preference.set", []byte(cmd.Key), string(callerID)),
		})
	})
	if err != nil {
		return SetUserPreferenceResult{}, err
	}

	// 5. return a Platform result type.
	return SetUserPreferenceResult{Preference: stored}, nil
}

// GetUserPreferencesQuery reads the calling user's own preferences.
type GetUserPreferencesQuery struct {
	Caller v1.Caller
}

// GetUserPreferencesResult is every preference the caller has set.
type GetUserPreferencesResult struct {
	Preferences []domain.UserPreference
}

// GetUserPreferences returns the calling user's preferences.
func (s *Service) GetUserPreferences(ctx context.Context, q GetUserPreferencesQuery) (GetUserPreferencesResult, error) {
	callerID, err := s.authenticateCaller(ctx, q.Caller)
	if err != nil {
		return GetUserPreferencesResult{}, err
	}
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionPreferenceRead,
		policy.Resource{Type: "preference"}, policy.PolicyContext{}); err != nil {
		return GetUserPreferencesResult{}, err
	}

	prefs, err := s.userPreferences.List(ctx, callerID)
	if err != nil {
		return GetUserPreferencesResult{}, err
	}
	return GetUserPreferencesResult{Preferences: prefs}, nil
}

// BoolPreference reports whether the caller has a preference set to JSON true.
//
// It is the shape every consumer of a flag preference wants, and it exists so
// each of them does not re-derive "unset means the default" differently. An
// unset key, an unreadable store and a non-boolean value all yield fallback:
// a preference must never be able to fail a request, because it only ever
// decides what to *show*.
//
// It deliberately does not authorise. It is an internal read on behalf of a
// caller already authenticated by the surface calling it, and gating a
// visibility hint behind a second permission check is how a toggle becomes an
// accidental access control (ADR 0058).
func (s *Service) BoolPreference(ctx context.Context, userID domain.UserID, key string, fallback bool) bool {
	pref, err := s.userPreferences.Get(ctx, userID, key)
	if err != nil {
		return fallback
	}
	var value bool
	if err := json.Unmarshal(pref.Value, &value); err != nil {
		return fallback
	}
	return value
}
