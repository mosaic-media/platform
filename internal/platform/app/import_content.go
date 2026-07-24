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
// under the content actions (ADR 0017) — this gate is the outer one.
const ActionContentImport policy.Action = "content.import"

// ImportContentCommand materialises one virtual content item — a ContentRef a
// search or catalog browse produced (ADR 0028) — into the graph. It names no
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

	// 2-3. authenticate the caller and authorize the invocation itself. Both
	// gates, in that order, are what enter is.
	az, err := s.enter(ctx, cmd.Caller, ActionContentImport, policy.Resource{Type: "content"})
	if err != nil {
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
	//
	// The module's context is a separate variable, not a shadow of ctx: it
	// carries the module's logger and telemetry surface (ADR 0059) and dies
	// with the span below, so Platform work after the call must not inherit it.
	mctx, span := moduleSpan(ctx, cmd.Ref.Provider, "import")
	result, err := capability.Import(mctx, s, v1.ImportRequest{
		Caller: cmd.Caller, Ref: cmd.Ref, Settings: settings,
	})
	failSpan(span, err)
	span.End()
	if err != nil {
		return v1.ImportResult{}, err
	}

	// 6b. fill in what plays (ADR 0073). The capability the ref named built the
	// tree; a metadata module fills no stream role, so without this a title
	// described by TMDB or Cinemeta would sit in the library permanently
	// unplayable while a stream source registered alongside it was never asked.
	//
	// Deliberately after the module's own span has ended and outside any error
	// path: it is best-effort, and an import that produced a tree has succeeded
	// whether or not anything could be found to play.
	if result.WorkID != "" {
		s.enrichStreams(ctx, cmd.Caller, result.WorkID, &result)

		// 6c. fill in what it looks like (ADR 0075). Same shape and same reasons
		// as the stream pass: a dedicated artwork source fills no metadata role
		// and is never named by a ref, so without this it would sit registered
		// and never be asked about the title it has the best art for.
		s.enrichArtwork(ctx, cmd.Caller, result.WorkID)
	}

	// 7. record that an import ran, for audit. The capability's own writes each
	// emit their content events; this marks the invocation itself — so it is
	// caused by the handler's span, not by the module span that has just ended.
	s.publishAuditEvent(ctx, "content.import.invoked", []byte(cmd.Ref.Provider), string(az.userID))

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
