// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package diagnostics

import (
	"encoding/json"
	"time"

	"github.com/mosaic-media/platform/internal/adapters/filesystem"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// bundleRedactedPlaceholder replaces any free-text detail a support bundle
// must not carry verbatim; support bundles should be fully anonymised while
// allowing program and Module identification.
const bundleRedactedPlaceholder = "[REDACTED FOR SUPPORT BUNDLE]"

// SupportBundle is the anonymised diagnostic export: program and
// Module/component identification, without personal data or secrets.
// ProgramID/ProgramVersion and each ComponentHealth's identifiers and
// states are never redacted — that is exactly the "program and Module
// identification" a bundle must still allow.
type SupportBundle struct {
	GeneratedAt    time.Time                `json:"generated_at"`
	ProgramID      string                   `json:"program_id"`
	ProgramVersion string                   `json:"program_version"`
	Components     []domain.ComponentHealth `json:"components"`
	// ModuleEgress records this deployment's layer-3 egress-containment posture
	// (ADR 0064, ADR 0080): whether the OS denies an extension module direct
	// network egress, and the honest sentence behind it. It is a structural fact
	// of the deployment, not personal data, so it is never redacted — and it is
	// here because "is module egress enforced?" is exactly a question a support
	// bundle exists to answer without the answer being claimed uniformly.
	ModuleEgress ModuleEgressPosture `json:"module_egress"`
}

// ModuleEgressPosture is the support bundle's plain projection of the extension
// package's EgressContainment, carried without importing that package so the
// diagnostics layer stays low-level.
type ModuleEgressPosture struct {
	Enforced bool   `json:"enforced"`
	Detail   string `json:"detail"`
}

// BuildSupportBundle assembles an anonymised SupportBundle from a health
// snapshot (e.g. from Registry.Snapshot). Support bundles are stricter than
// local logs: any ComponentHealth not explicitly classed RedactionNone has
// its free-text DegradedReason replaced outright, even though the same
// detail might be permitted (redacted-but-present) in a local log entry.
// Component identifiers, lifecycle, health state, last successful check,
// last failure category and dependency health are structural facts, not
// free text, so they are never redacted.
func BuildSupportBundle(programID, programVersion string, components []domain.ComponentHealth, egress ModuleEgressPosture, generatedAt time.Time) SupportBundle {
	anonymised := make([]domain.ComponentHealth, len(components))
	for i, c := range components {
		if c.RedactionClass != domain.RedactionNone {
			c.DegradedReason = bundleRedactedPlaceholder
		}
		anonymised[i] = c
	}
	return SupportBundle{
		GeneratedAt:    generatedAt,
		ProgramID:      programID,
		ProgramVersion: programVersion,
		Components:     anonymised,
		ModuleEgress:   egress,
	}
}

// WriteSupportBundle serializes bundle as indented JSON and writes it
// atomically to path (via internal/adapters/filesystem, so a crash never
// leaves a half-written bundle on disk). This is a basic mechanism: a
// single anonymised file, not a multi-file archive.
func WriteSupportBundle(path string, bundle SupportBundle) error {
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return filesystem.WriteFileAtomic(path, data)
}
