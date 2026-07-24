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

// InstallExtension/UninstallExtension are the authorized entry points to the
// runtime extension lifecycle (ADR 0081). Their boundary — refusing an unknown
// or ungranted caller — is asserted by the boundary conformance suite; these
// tests assert the other half: an authorized call delegates to the injected
// manager, the result is mapped back, input is validated, and a Platform built
// without a manager degrades rather than panicking.

// fakeExtensionManager records what the Service delegated to it.
type fakeExtensionManager struct {
	installRepo string
	installID   string
	uninstalled string
	set         []domain.InstalledExtension
}

func (f *fakeExtensionManager) Install(_ context.Context, repo, id string) (domain.InstalledExtension, error) {
	f.installRepo, f.installID = repo, id
	rec := domain.InstalledExtension{ModuleID: id, Repository: repo, Version: "v1.2.3", SignedBy: repo}
	f.set = append(f.set, rec)
	return rec, nil
}

func (f *fakeExtensionManager) Uninstall(_ context.Context, id string) error {
	f.uninstalled = id
	return nil
}

func (f *fakeExtensionManager) InstalledExtensions(context.Context) ([]domain.InstalledExtension, error) {
	return f.set, nil
}

func (f *fakeExtensionManager) Available(context.Context) ([]app.ExtensionCatalogueEntry, error) {
	return nil, nil
}

// extAdminFixture builds a Service with the given manager and an authenticated
// administrator session (extension.manage is an administrator action).
func extAdminFixture(t *testing.T, ext app.ExtensionManager) (*app.Service, v1.Caller) {
	t.Helper()
	now := time.Date(2026, 7, 24, 12, 0, 0, 0, time.UTC)
	db := newFakeDB()
	svc := newTestServiceWithExtensions(db, &trace{}, now, ext)
	db.seedUser(domain.User{ID: "u-1", Username: "admin", Status: domain.UserActive, CreatedAt: now, UpdatedAt: now})
	db.seedSession("s-1", "u-1", now)
	db.seedRole("u-1", adminRole())
	return svc, v1.Caller{Session: "s-1"}
}

func TestInstallExtensionDelegatesAndMapsTheResult(t *testing.T) {
	ext := &fakeExtensionManager{}
	svc, caller := extAdminFixture(t, ext)

	got, err := svc.InstallExtension(context.Background(), app.InstallExtensionCommand{
		Caller: caller, Repository: "mosaic-official", ModuleID: "stremio",
	})
	if err != nil {
		t.Fatalf("InstallExtension: %v", err)
	}
	if ext.installRepo != "mosaic-official" || ext.installID != "stremio" {
		t.Errorf("delegated the wrong args: repo=%q id=%q", ext.installRepo, ext.installID)
	}
	if got.ModuleID != "stremio" || got.Version != "v1.2.3" || got.SignedBy != "mosaic-official" {
		t.Errorf("result not mapped from the manager's record: %+v", got)
	}
}

func TestUninstallExtensionDelegates(t *testing.T) {
	ext := &fakeExtensionManager{}
	svc, caller := extAdminFixture(t, ext)

	if err := svc.UninstallExtension(context.Background(), app.UninstallExtensionCommand{
		Caller: caller, ModuleID: "stremio",
	}); err != nil {
		t.Fatalf("UninstallExtension: %v", err)
	}
	if ext.uninstalled != "stremio" {
		t.Errorf("did not delegate the uninstall: %q", ext.uninstalled)
	}
}

func TestInstallExtensionValidatesInput(t *testing.T) {
	svc, caller := extAdminFixture(t, &fakeExtensionManager{})

	_, err := svc.InstallExtension(context.Background(), app.InstallExtensionCommand{
		Caller: caller, Repository: "", ModuleID: "stremio",
	})
	if got := contracts.CategoryOf(err); got != contracts.InvalidArgument {
		t.Errorf("a missing repository should be InvalidArgument, got %q (%v)", got, err)
	}
}

func TestExtensionManagementUnavailableWithoutAManager(t *testing.T) {
	// A Service wired with no extension manager still authorises, then reports the
	// capability unavailable rather than dereferencing a nil.
	svc, caller := extAdminFixture(t, nil)

	_, err := svc.InstallExtension(context.Background(), app.InstallExtensionCommand{
		Caller: caller, Repository: "mosaic-official", ModuleID: "stremio",
	})
	if got := contracts.CategoryOf(err); got != contracts.Unavailable {
		t.Errorf("install with no manager should be Unavailable, got %q (%v)", got, err)
	}
}
