// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// A user's own settings (ADR 0058). Expert mode is the first; the store is
// general because it will not be the last.

func TestSetAndReadUserPreference(t *testing.T) {
	ctx := context.Background()
	svc, db, tr, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	res, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: caller, Key: domain.PreferenceExpertMode, Value: []byte("true"),
	})
	if err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}
	if res.Preference.Key != domain.PreferenceExpertMode {
		t.Fatalf("stored key = %q", res.Preference.Key)
	}
	if !traced(tr, "user_preferences.set:"+domain.PreferenceExpertMode) {
		t.Fatalf("preference was not written: %v", tr.snapshot())
	}
	// State and its event commit together, like every other write.
	if !db.outboxHas("preference.set") {
		t.Fatalf("no preference.set event: %v", db.outboxTypes())
	}

	got, err := svc.GetUserPreferences(ctx, app.GetUserPreferencesQuery{Caller: caller})
	if err != nil {
		t.Fatalf("GetUserPreferences: %v", err)
	}
	if len(got.Preferences) != 1 || string(got.Preferences[0].Value) != "true" {
		t.Fatalf("read back %+v", got.Preferences)
	}
}

// TestPreferenceIsScopedToTheCaller is the property that makes the weak
// permission safe: there is no target-user parameter, so holding
// preference.write grants a user nothing over anybody else.
func TestPreferenceIsScopedToTheCaller(t *testing.T) {
	ctx := context.Background()
	svc, db, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	if _, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: caller, Key: domain.PreferenceExpertMode, Value: []byte("true"),
	}); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}

	// A second user with the same key set to something else must not see the
	// first user's value.
	db.seedUser(domain.User{ID: "u-2", Username: "other", Status: domain.UserActive})
	db.seedSession("s-2", "u-2", time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC))
	db.seedRole("u-2", adminRole())

	other := v1.Caller{Session: "s-2"}
	got, err := svc.GetUserPreferences(ctx, app.GetUserPreferencesQuery{Caller: other})
	if err != nil {
		t.Fatalf("GetUserPreferences(other): %v", err)
	}
	if len(got.Preferences) != 0 {
		t.Fatalf("a user read another user's preferences: %+v", got.Preferences)
	}
}

func TestSetUserPreferenceRejectsMalformedInput(t *testing.T) {
	ctx := context.Background()
	svc, _, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	for name, cmd := range map[string]app.SetUserPreferenceCommand{
		"no key": {Caller: caller, Key: "", Value: []byte("true")},
		// jsonb would reject this at the driver, with a message about the
		// database rather than about the request.
		"not json": {Caller: caller, Key: "ui.theme", Value: []byte("{not json")},
	} {
		_, err := svc.SetUserPreference(ctx, cmd)
		if contracts.CategoryOf(err) != contracts.InvalidArgument {
			t.Fatalf("%s: category = %v, want invalid_argument", name, contracts.CategoryOf(err))
		}
	}
}

func TestUserPreferenceRequiresAnAuthenticatedCaller(t *testing.T) {
	ctx := context.Background()
	svc, _, _, _ := importFixture(t)

	_, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: v1.Caller{Session: "nope"}, Key: domain.PreferenceExpertMode, Value: []byte("true"),
	})
	if contracts.CategoryOf(err) != contracts.Unauthenticated {
		t.Fatalf("category = %v, want unauthenticated", contracts.CategoryOf(err))
	}
}

// TestBoolPreferenceFallsBackRatherThanFailing is the rule that keeps a
// preference from ever breaking a request: it decides what to *show*, so an
// unset key, a wrong type or an unreadable store must all yield the default.
func TestBoolPreferenceFallsBackRatherThanFailing(t *testing.T) {
	ctx := context.Background()
	svc, _, _, session := importFixture(t)
	caller := v1.Caller{Session: string(session)}

	// Unset.
	if got := svc.BoolPreference(ctx, "u-1", domain.PreferenceExpertMode, false); got {
		t.Fatal("an unset preference must take the fallback")
	}
	if got := svc.BoolPreference(ctx, "u-1", domain.PreferenceExpertMode, true); !got {
		t.Fatal("an unset preference must take the fallback, including a true one")
	}

	// Set to the wrong type.
	if _, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: caller, Key: "ui.theme", Value: []byte(`"dark"`),
	}); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}
	if got := svc.BoolPreference(ctx, "u-1", "ui.theme", false); got {
		t.Fatal("a non-boolean value must take the fallback, not panic or coerce")
	}

	// Set properly.
	if _, err := svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
		Caller: caller, Key: domain.PreferenceExpertMode, Value: []byte("true"),
	}); err != nil {
		t.Fatalf("SetUserPreference: %v", err)
	}
	if got := svc.BoolPreference(ctx, "u-1", domain.PreferenceExpertMode, false); !got {
		t.Fatal("a preference set to true must read back true")
	}
}
