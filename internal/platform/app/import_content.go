// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
	"github.com/mosaic-media/mosaic-platform/internal/platform/policy"
	v1 "github.com/mosaic-media/mosaic-sdk/contracts/platform/v1"
)

// ActionContentImport is the policy action evaluated for invoking a capability
// to import content. It gates who may trigger an import at all. The capability
// then acts as the same caller, so each node it creates is authorised again
// under the content actions (ADR 0017) — this gate is the outer one.
const ActionContentImport policy.Action = "content.import"

// ImportContentCommand materialises one virtual content item — a ContentRef a
// search or catalog browse produced (ADR 0028) — into the graph. It names no
// capability id of its own: the ref carries its Provider, the module that can
// materialise it. It is a Platform command a transport issues (the GraphQL
// importContent mutation), deliberately not part of the published
// ContentService: a capability is invoked by this command, it does not call it.
type ImportContentCommand struct {
	Caller v1.Caller
	Ref    v1.ContentRef
}

func validateImportContentCommand(cmd ImportContentCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.Ref.Provider == "" {
		return contracts.NewError(contracts.InvalidArgument, "ref provider is required")
	}
	if cmd.Ref.NativeID == "" || cmd.Ref.NativeType == "" {
		return contracts.NewError(contracts.InvalidArgument, "ref native id and type are required")
	}
	return nil
}

// ImportContent invokes a registered capability to source content into the
// graph. It follows the command boundary up to authorization, then hands the
// capability the Service itself as its ContentService and the original Caller,
// so every write the capability makes re-enters the same command order and is
// authorised as the invoking user (ADR 0017).
//
// It opens no UnitOfWork of its own: the capability's service calls each open
// theirs, one transaction per write. A capability that fails partway leaves
// the writes it already committed in place — import is not atomic across the
// whole tree, by the same reasoning that lets it search between writes.
func (s *Service) ImportContent(ctx context.Context, cmd ImportContentCommand) (v1.ImportResult, error) {
	// 1. validate command shape.
	if err := validateImportContentCommand(cmd); err != nil {
		return v1.ImportResult{}, err
	}

	// 2. authenticate caller.
	callerID, err := s.authenticateCaller(ctx, cmd.Caller)
	if err != nil {
		return v1.ImportResult{}, err
	}

	// 3. authorize the invocation itself.
	if err := s.authorize(ctx, policy.Subject{UserID: callerID}, ActionContentImport, policy.Resource{Type: "content"}, policy.PolicyContext{}); err != nil {
		return v1.ImportResult{}, err
	}

	// 4. resolve the capability the ref names as its materialiser.
	capability, ok := s.lookupCapability(cmd.Ref.Provider)
	if !ok {
		return v1.ImportResult{}, contracts.NewError(contracts.NotFound, "no capability registered under id "+cmd.Ref.Provider)
	}

	// 5. read the module's user-managed settings so it is invoked with the
	// configuration a user set for it (ADR 0021). Absent settings read back as
	// an empty document, never an error.
	settings, err := s.readModuleSettings(ctx, cmd.Ref.Provider)
	if err != nil {
		return v1.ImportResult{}, err
	}

	// 6. invoke it, forwarding the caller so it acts as the invoking user and
	// passing the Service as the ContentService it drives.
	result, err := capability.Import(ctx, s, v1.ImportRequest{
		Caller: cmd.Caller, Ref: cmd.Ref, Settings: settings,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}

	// 7. record that an import ran, for audit. The capability's own writes each
	// emit their content events; this marks the invocation itself.
	s.publishAuditEvent(ctx, "content.import.invoked", []byte(cmd.Ref.Provider), string(callerID))

	return result, nil
}

// lookupCapability resolves a capability id against the registry, tolerating a
// Service constructed without one (every test that does not exercise import).
func (s *Service) lookupCapability(id string) (v1.Capability, bool) {
	if s.capabilities == nil {
		return nil, false
	}
	return s.capabilities.Lookup(id)
}

// readModuleSettings returns the module's settings document, or an empty object
// when no settings store is wired (a Service built without one) — so a
// capability that ignores settings is unaffected.
func (s *Service) readModuleSettings(ctx context.Context, moduleID string) ([]byte, error) {
	if s.moduleSettings == nil {
		return []byte("{}"), nil
	}
	settings, err := s.moduleSettings.Get(ctx, moduleID)
	if err != nil {
		return nil, err
	}
	if len(settings.Settings) == 0 {
		return []byte("{}"), nil
	}
	return settings.Settings, nil
}
