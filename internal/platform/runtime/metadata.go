// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime

import (
	"github.com/mosaic-media/platform/internal/composition/builtin"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// PlatformVersion is the Platform build identifier. Nothing stamps it from a
// release tag or commit yet, so it is a fixed literal recorded in one place
// rather than a value invented ad hoc at each call site.
const PlatformVersion = "v0.0.0-foundation"

// ModuleMetadata is one built-in Module's identity, mirrored from
// builtin.Manifest for the Generation metadata surface.
type ModuleMetadata struct {
	ID       string
	Version  string
	Fulfills []string
}

// GenerationMetadata identifies this build for the Supervisor: Platform
// version, contract version, built-in Modules and assets.
type GenerationMetadata struct {
	PlatformVersion string
	ContractID      string
	ContractVersion string
	Modules         []ModuleMetadata
	// Assets lists build/Shell assets bundled into this Generation. This
	// repository has no build pipeline or Shell yet, so it is always
	// empty — a documented gap, not a fabricated asset list.
	Assets []string
}

// BuildGenerationMetadata assembles GenerationMetadata from the registered
// built-in Modules, the contract ID/version and the composition root's
// builtin.Registry.
func BuildGenerationMetadata(modules *builtin.Registry) GenerationMetadata {
	manifests := modules.Manifests()
	moduleMetadata := make([]ModuleMetadata, len(manifests))
	for i, m := range manifests {
		moduleMetadata[i] = ModuleMetadata{
			ID:       m.ID,
			Version:  m.Version,
			Fulfills: append([]string(nil), m.Fulfills...),
		}
	}
	return GenerationMetadata{
		PlatformVersion: PlatformVersion,
		ContractID:      contracts.ContractID,
		ContractVersion: contracts.ContractVersion,
		Modules:         moduleMetadata,
		Assets:          []string{},
	}
}
