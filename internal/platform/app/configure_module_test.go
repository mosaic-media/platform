// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/platform/app"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// ConfigureModule and GetModuleSettings are the user-managed module settings
// path (ADR 0021): a user sets a registered module's settings document, and it
// is stored, read back, and forwarded to the module on invocation.

func TestConfigureModule(t *testing.T) {
	ctx := context.Background()

	t.Run("stores settings for a registered module and reads them back", func(t *testing.T) {
		cap := &recordingCapability{id: "stremio"}
		svc, _, tr, session := importFixture(t, cap)
		caller := v1.Caller{Session: string(session)}

		res, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
			Caller: caller, ModuleID: "stremio", Settings: []byte(`{"addons":["https://cinemeta.example/manifest.json"]}`),
		})
		if err != nil {
			t.Fatalf("ConfigureModule: %v", err)
		}
		if res.ModuleID != "stremio" {
			t.Fatalf("result module id = %q", res.ModuleID)
		}
		if !traced(tr, "module_settings.set:stremio") {
			t.Fatalf("settings were not written: %v", tr.snapshot())
		}

		got, err := svc.GetModuleSettings(ctx, app.GetModuleSettingsQuery{Caller: caller, ModuleID: "stremio"})
		if err != nil {
			t.Fatalf("GetModuleSettings: %v", err)
		}
		if string(got.Settings) != `{"addons":["https://cinemeta.example/manifest.json"]}` {
			t.Fatalf("read back settings = %q", got.Settings)
		}
	})

	t.Run("settings reach the module on the next import", func(t *testing.T) {
		cap := &recordingCapability{id: "stremio"}
		svc, _, _, session := importFixture(t, cap)
		caller := v1.Caller{Session: string(session)}

		if _, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
			Caller: caller, ModuleID: "stremio", Settings: []byte(`{"addons":["x"]}`),
		}); err != nil {
			t.Fatalf("ConfigureModule: %v", err)
		}
		if _, err := svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: testRef("stremio", "movie", "tt1")}); err != nil {
			t.Fatalf("ImportContent: %v", err)
		}
		if string(cap.gotSettings) != `{"addons":["x"]}` {
			t.Fatalf("module saw settings %q, want the configured document", cap.gotSettings)
		}
	})

	t.Run("an unknown module id is NotFound", func(t *testing.T) {
		svc, _, _, session := importFixture(t)
		_, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
			Caller: v1.Caller{Session: string(session)}, ModuleID: "nope", Settings: []byte(`{}`),
		})
		if got := contracts.CategoryOf(err); got != contracts.NotFound {
			t.Fatalf("category = %s, want NotFound", got)
		}
	})

	t.Run("invalid JSON settings are InvalidArgument", func(t *testing.T) {
		svc, _, _, session := importFixture(t, &recordingCapability{id: "stremio"})
		_, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
			Caller: v1.Caller{Session: string(session)}, ModuleID: "stremio", Settings: []byte(`{not json`),
		})
		if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
			t.Fatalf("category = %s, want InvalidArgument", got)
		}
	})

	t.Run("an unauthorised caller cannot configure", func(t *testing.T) {
		now := time.Date(2026, 7, 20, 12, 0, 0, 0, time.UTC)
		db := newFakeDB()
		registry := app.NewCapabilityRegistry()
		registry.Register(&recordingCapability{id: "stremio"})
		svc := newTestServiceWithCapabilities(db, &trace{}, now, registry)
		// A caller with a session but no role granting module.configure.
		db.seedUser(domain.User{ID: "u-2", Username: "viewer", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
		db.seedSession("s-2", "u-2", now)

		_, err := svc.ConfigureModule(ctx, app.ConfigureModuleCommand{
			Caller: v1.Caller{Session: "s-2"}, ModuleID: "stremio", Settings: []byte(`{}`),
		})
		if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
			t.Fatalf("category = %s, want PermissionDenied", got)
		}
	})
}
