package runtime

import (
	"github.com/mosaic-media/mosaic-platform/internal/composition/builtin"
	"github.com/mosaic-media/mosaic-platform/internal/platform/contracts"
)

// PlatformVersion is a first-cut Platform build identifier. A real build
// pipeline (MEG-005 §14 — Supervisor Model's Build Pipeline) would stamp
// this from a release tag or commit; until that exists, it is a fixed
// literal recorded here so Generation metadata has a real, if provisional,
// value rather than one invented ad hoc at each call site.
const PlatformVersion = "v0.0.0-foundation"

// ModuleMetadata is one built-in Module's identity, mirrored from
// builtin.Manifest for the Generation metadata surface.
type ModuleMetadata struct {
	ID       string
	Version  string
	Fulfills []string
}

// GenerationMetadata identifies this build for the Supervisor (MEG-015
// §10 — Generation metadata: "Identifies Platform version, contract
// version, built-in Modules and assets").
type GenerationMetadata struct {
	PlatformVersion string
	ContractID      string
	ContractVersion string
	Modules         []ModuleMetadata
	// Assets lists build/Shell assets bundled into this Generation
	// (MIP-006 — Generation Composition Protocol: "Platform, Shell and
	// admitted Modules, plus manifests, assets and signatures"). This
	// repository has no build pipeline or Shell yet, so it is always
	// empty — a documented gap, not a fabricated asset list.
	Assets []string
}

// BuildGenerationMetadata assembles GenerationMetadata from the registered
// built-in Modules (MEG-015 §03's ContractID/ContractVersion plus the
// composition root's builtin.Registry).
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
