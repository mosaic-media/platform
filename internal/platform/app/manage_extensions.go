// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionExtensionManage is the policy action for installing and uninstalling
// extension modules (ADR 0081). It is administrator-level, beside
// module.configure: deciding which extension modules run is the same kind of
// authority as configuring the ones that do.
const ActionExtensionManage policy.Action = "extension.manage"

// ExtensionManager is the runtime lifecycle of extension modules the Service
// drives on a user's authorized request (ADR 0079, ADR 0081): install and
// uninstall change the durable set and adopt or drop the process live, and the
// installed list is what a settings surface reads.
//
// It is an interface here so the app package depends only on it, not on the
// extension adapter it fronts. The composition root implements it
// (internal/composition/extensions.Manager) and injects it — the same shape by
// which the Service holds every other capability it does not itself contain.
type ExtensionManager interface {
	// Install fetches, verifies, spawns and records a module from a trusted
	// repository; the record is the durable install (ADR 0081).
	Install(ctx context.Context, repository, moduleID string) (domain.InstalledExtension, error)
	// Uninstall stops a module, makes it unresolvable and drops its record. It is
	// idempotent.
	Uninstall(ctx context.Context, moduleID string) error
	// InstalledExtensions is the durable installed set, for the settings surface.
	InstalledExtensions(ctx context.Context) ([]domain.InstalledExtension, error)
}

// InstallExtensionCommand installs one module from one trusted repository.
type InstallExtensionCommand struct {
	Caller     v1.Caller
	Repository string
	ModuleID   string
}

// InstalledExtension is the Platform result for an installed module — its
// identity and provenance (ADR 0081, ADR 0065).
type InstalledExtension struct {
	ModuleID   string
	Repository string
	Version    string
	SignedBy   string
}

// UninstallExtensionCommand removes one installed module.
type UninstallExtensionCommand struct {
	Caller   v1.Caller
	ModuleID string
}

// ListInstalledExtensionsQuery reads the durable installed set.
type ListInstalledExtensionsQuery struct {
	Caller v1.Caller
}

func fromDomainInstalled(d domain.InstalledExtension) InstalledExtension {
	return InstalledExtension{
		ModuleID:   d.ModuleID,
		Repository: d.Repository,
		Version:    d.Version,
		SignedBy:   d.SignedBy,
	}
}

// InstallExtension installs an extension module on a user's request, following
// the command boundary. The mechanics — download, signature and digest
// verification, spawn, and recording the durable install — are the injected
// ExtensionManager's (ADR 0081); this method's job is the boundary: validate,
// authenticate, authorize, then delegate.
//
// It does not open a UnitOfWork. Installing is a side-effectful process
// operation, not a pure state write, and its persistence lives with the manager
// that also spawns the process; the manager rolls the process back if the record
// cannot be written, so the two do not drift. The act is audited through the
// manager's telemetry rather than the outbox.
func (s *Service) InstallExtension(ctx context.Context, cmd InstallExtensionCommand) (InstalledExtension, error) {
	if cmd.Caller.Session == "" {
		return InstalledExtension{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.Repository == "" || cmd.ModuleID == "" {
		return InstalledExtension{}, contracts.NewError(contracts.InvalidArgument, "repository and module id are required")
	}
	if _, err := s.enter(ctx, cmd.Caller, ActionExtensionManage, policy.Resource{Type: "extension"}); err != nil {
		return InstalledExtension{}, err
	}
	if s.extensions == nil {
		return InstalledExtension{}, contracts.NewError(contracts.Unavailable, "extension management is not available in this Platform")
	}
	installed, err := s.extensions.Install(ctx, cmd.Repository, cmd.ModuleID)
	if err != nil {
		return InstalledExtension{}, err
	}
	return fromDomainInstalled(installed), nil
}

// UninstallExtension removes an installed extension on a user's request. Like
// install, the boundary is here and the mechanics are the manager's. Uninstall
// is idempotent, so removing one that is not installed authorises and returns
// without error.
func (s *Service) UninstallExtension(ctx context.Context, cmd UninstallExtensionCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.ModuleID == "" {
		return contracts.NewError(contracts.InvalidArgument, "module id is required")
	}
	if _, err := s.enter(ctx, cmd.Caller, ActionExtensionManage, policy.Resource{Type: "extension"}); err != nil {
		return err
	}
	if s.extensions == nil {
		return contracts.NewError(contracts.Unavailable, "extension management is not available in this Platform")
	}
	return s.extensions.Uninstall(ctx, cmd.ModuleID)
}

// ListInstalledExtensions returns the durable installed set for a settings
// surface. It reads what modules are installed, which is administrator
// information — the same read as opening the settings that manage them — so it
// authorises ActionModuleRead rather than being public.
func (s *Service) ListInstalledExtensions(ctx context.Context, q ListInstalledExtensionsQuery) ([]InstalledExtension, error) {
	if q.Caller.Session == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionModuleRead, policy.Resource{Type: "extension"}); err != nil {
		return nil, err
	}
	if s.extensions == nil {
		return nil, nil
	}
	records, err := s.extensions.InstalledExtensions(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]InstalledExtension, 0, len(records))
	for _, r := range records {
		out = append(out, fromDomainInstalled(r))
	}
	return out, nil
}
