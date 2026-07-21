// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"context"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

func TestGetActiveConfigVersionReturnsNotFoundBeforeAnyActivation(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.GetActiveConfigVersion(context.Background(), app.GetActiveConfigVersionQuery{CallerSessionID: adminSession})
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.NotFound)
	}
}

func TestGetActiveConfigVersionReturnsActivatedVersion(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)
	ctx := context.Background()

	drafted, err := svc.DraftConfigVersion(ctx, app.DraftConfigVersionCommand{
		CallerSessionID: adminSession,
		Payload:         []byte(`{"runtime.log_level":"debug"}`),
	})
	if err != nil {
		t.Fatalf("DraftConfigVersion() error = %v", err)
	}
	if _, err := svc.ValidateConfigVersion(ctx, app.ValidateConfigVersionCommand{CallerSessionID: adminSession, ConfigVersionID: drafted.Version.ID}); err != nil {
		t.Fatalf("ValidateConfigVersion() error = %v", err)
	}
	if _, err := svc.ActivateConfigVersion(ctx, app.ActivateConfigVersionCommand{CallerSessionID: adminSession, ConfigVersionID: drafted.Version.ID}); err != nil {
		t.Fatalf("ActivateConfigVersion() error = %v", err)
	}

	result, err := svc.GetActiveConfigVersion(ctx, app.GetActiveConfigVersionQuery{CallerSessionID: adminSession})
	if err != nil {
		t.Fatalf("GetActiveConfigVersion() error = %v", err)
	}
	if result.Version.ID != drafted.Version.ID || result.Version.Status != domain.ConfigActive {
		t.Fatalf("result.Version = %+v, want the activated version", result.Version)
	}
}

func TestGetConfigVersionReturnsNotFoundForUnknownID(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	adminSession := seedAdminCaller(db, testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.GetConfigVersion(context.Background(), app.GetConfigVersionQuery{
		CallerSessionID: adminSession,
		ConfigVersionID: "does-not-exist",
	})
	if got := contracts.CategoryOf(err); got != contracts.NotFound {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.NotFound)
	}
}

func TestGetConfigVersionDeniedByPolicy(t *testing.T) {
	db := newFakeDB()
	tr := &trace{}
	db.seedSession("session-nobody", "user-nobody", testNow)
	svc := newTestService(db, tr, testNow)

	_, err := svc.GetConfigVersion(context.Background(), app.GetConfigVersionQuery{
		CallerSessionID: "session-nobody",
		ConfigVersionID: "cv-1",
	})
	if got := contracts.CategoryOf(err); got != contracts.PermissionDenied {
		t.Fatalf("CategoryOf(err) = %s, want %s", got, contracts.PermissionDenied)
	}
}
