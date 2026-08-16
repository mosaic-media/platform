// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ActionContentImport is the policy action evaluated for invoking a capability
// to import content. It gates who may trigger an import at all. The capability
// then acts as the same caller, so each node it creates is authorised again
// under the content actions (platform#13) — this gate is the outer one.
const ActionContentImport policy.Action = "content.import"

// ImportContentCommand materialises one virtual content item — a ContentRef a
// search or catalog browse produced (platform#18) — into the graph. It names no
// capability id of its own: the ref carries its Provider, the module that can
// materialise it. It is a Platform command a transport issues (the session
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
// authorised as the invoking user (platform#13).
//
// It opens no UnitOfWork of its own: the capability's service calls each open
// theirs, one transaction per write. A capability that fails partway leaves
// the writes it already committed in place — import is not atomic across the
// whole tree, by the same reasoning that lets it search between writes.
func (s *Service) ImportContent(ctx context.Context, cmd ImportContentCommand) (v1.ImportResult, error) {
	if err := validateImportContentCommand(cmd); err != nil {
		return v1.ImportResult{}, err
	}

	az, err := s.enter(ctx, cmd.Caller, ActionContentImport, policy.Resource{Type: "content"})
	if err != nil {
		return v1.ImportResult{}, err
	}

	capability, ok := s.lookupCapability(cmd.Ref.Provider)
	if !ok {
		return v1.ImportResult{}, contracts.NewError(contracts.NotFound, "no capability registered under id "+cmd.Ref.Provider)
	}

	// The configuration a user set for this module (platform#17).
	settings, err := s.readModuleSettings(ctx, cmd.Ref.Provider)
	if err != nil {
		return v1.ImportResult{}, err
	}

	result, err := s.invokeImport(ctx, capability, cmd, settings)
	if err != nil {
		return v1.ImportResult{}, err
	}

	if result.WorkID != "" {
		s.enrichImported(ctx, cmd.Caller, cmd.Ref, &result)
	}

	// Record that an import ran, for audit. The capability's own writes each
	// emit their content events; this marks the invocation itself — so it is
	// caused by the handler's span, not by the module span that has just ended.
	s.publishAuditEvent(ctx, "content.import.invoked", []byte(cmd.Ref.Provider), string(az.userID))

	return result, nil
}

// invokeImport runs the capability's own import inside a module span.
//
// The module's context is a separate variable, not a shadow of ctx: it carries
// the module's logger and telemetry surface (sdk#5) and dies with the span, so
// Platform work after the call must not inherit it.
func (s *Service) invokeImport(ctx context.Context, capability v1.Capability, cmd ImportContentCommand, settings []byte) (v1.ImportResult, error) {
	mctx, span := moduleSpan(ctx, cmd.Ref.Provider, "import")
	result, err := capability.Import(mctx, s, v1.ImportRequest{
		Caller: cmd.Caller, Ref: cmd.Ref, Settings: settings,
	})
	failSpan(span, err)
	span.End()
	return result, err
}

// enrichImported fills in what the capability the ref named could not: what the
// title is and what shape it is (platform#62), what plays (platform#46), and what
// it looks like (sdk#6). A metadata module fills no stream role, and an artwork
// source is never named by a ref, so without these passes a described title would
// sit permanently unplayable while a stream source registered alongside was never
// asked — and the source with the best art for it never asked either.
//
// The metadata pass must run first because both of the others read the tree: the
// stream pass fills items with no Parts, so an episode it adds is enriched in the
// same import, and the artwork pass walks the seasons it may have just created.
// It is also what makes a re-import worth anything — every module dedups before
// writing and returns AlreadyKnown, so without it the second import of a title
// refreshes everything about it except its shape and its description.
//
// Called after the module's own span has ended and outside any error path: these
// are best-effort, and an import that produced a tree has succeeded whether or
// not anything could be found to play.
func (s *Service) enrichImported(ctx context.Context, caller v1.Caller, ref v1.ContentRef, result *v1.ImportResult) {
	s.enrichMetadata(ctx, caller, ref, result.WorkID)
	s.enrichStreams(ctx, caller, result.WorkID, result)
	s.enrichArtwork(ctx, caller, result.WorkID)
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
