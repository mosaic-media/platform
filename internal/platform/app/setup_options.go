// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// SetupOptions is what the setup doorway can offer a household to choose from
// (platform#54).
//
// Today that is one list: where its streams come from. The other steps that
// remain — naming the server and creating the owner — need nothing read from
// anywhere.
type SetupOptions struct {
	// StreamSources are the extension modules in the trusted repository that
	// fill the stream role, in the order the repository lists them.
	StreamSources []ExtensionCatalogueEntry
	// Problem says why the list is empty when it should not have been. An
	// unreachable repository during setup is common and recoverable — a slow
	// connection, a first boot before DNS settles — and a step that silently
	// offers nothing reads as a step that has no options.
	Problem string
}

// SetupOptions reads the choices the setup doorway offers.
//
// It is unauthenticated, and the only unauthenticated read that leaves this
// machine. Two things bound it: it refuses on a claimed server, so it is
// reachable only in the window a claim is, and the bootstrap that reaches it is
// rate-limited as the one pre-authentication surface (platform#57). What it
// discloses is a public signed index published on the internet, so the caller
// learns nothing about this install they could not learn by fetching the same
// file themselves.
//
// It never fails. A repository that will not answer costs the stream-source
// options and not the claim: a household can name its server, create its owner
// and add a source later from Settings.
func (s *Service) SetupOptions(ctx context.Context) SetupOptions {
	if !s.ServerState(ctx).Claimable() {
		return SetupOptions{}
	}
	if s.extensions == nil {
		return SetupOptions{}
	}
	entries, err := s.extensions.Available(ctx)
	if err != nil {
		telemetry.From(ctx).For("auth").Warn(
			"the module repository could not be reached during setup; offering no stream sources",
			telemetry.Err(err))
		return SetupOptions{
			Problem: "Mosaic could not reach its module repository, so it cannot offer a stream source " +
				"right now. Finish setting up and add one from Settings.",
		}
	}
	sources := make([]ExtensionCatalogueEntry, 0, len(entries))
	for _, e := range entries {
		if providesRole(e, v1.RoleStream) {
			sources = append(sources, e)
		}
	}
	return SetupOptions{StreamSources: sources}
}

// providesRole reports whether a catalogued module fills role.
//
// Match on the declared role rather than on the module's id: the setup step asks
// a question about capability, and a hard-coded list of module names would be a
// second, silent copy of the registry that goes stale the first time it grows.
func providesRole(e ExtensionCatalogueEntry, role v1.Role) bool {
	for _, r := range e.Provides {
		if v1.Role(r) == role {
			return true
		}
	}
	return false
}
