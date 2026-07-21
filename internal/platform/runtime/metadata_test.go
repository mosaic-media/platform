// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package runtime_test

import (
	"testing"

	"github.com/mosaic-media/platform/internal/composition/builtin"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/runtime"
)

type fakeModule struct{ manifest builtin.Manifest }

func (m fakeModule) Manifest() builtin.Manifest { return m.manifest }

func TestBuildGenerationMetadataReflectsRegisteredModules(t *testing.T) {
	registry := builtin.NewRegistry()
	registry.Register(fakeModule{manifest: builtin.Manifest{
		ID: "mosaic.platform.module.postgres", Version: "v1",
		Fulfills: []string{"UnitOfWork", "HealthProbe"},
	}})

	metadata := runtime.BuildGenerationMetadata(registry)

	if metadata.PlatformVersion == "" {
		t.Fatal("expected a non-empty PlatformVersion")
	}
	if metadata.ContractID != contracts.ContractID {
		t.Fatalf("ContractID = %q, want %q", metadata.ContractID, contracts.ContractID)
	}
	if metadata.ContractVersion != contracts.ContractVersion {
		t.Fatalf("ContractVersion = %q, want %q", metadata.ContractVersion, contracts.ContractVersion)
	}
	if len(metadata.Modules) != 1 {
		t.Fatalf("len(Modules) = %d, want 1", len(metadata.Modules))
	}
	if metadata.Modules[0].ID != "mosaic.platform.module.postgres" {
		t.Fatalf("Modules[0].ID = %q", metadata.Modules[0].ID)
	}
	if len(metadata.Modules[0].Fulfills) != 2 {
		t.Fatalf("Modules[0].Fulfills = %v", metadata.Modules[0].Fulfills)
	}
	if metadata.Assets == nil {
		t.Fatal("expected Assets to be an empty slice, not nil (documented gap, not omitted)")
	}
}

func TestBuildGenerationMetadataWithNoModules(t *testing.T) {
	metadata := runtime.BuildGenerationMetadata(builtin.NewRegistry())
	if len(metadata.Modules) != 0 {
		t.Fatalf("len(Modules) = %d, want 0", len(metadata.Modules))
	}
}
