package diagnostics

import (
	"encoding/json"
	"time"

	"github.com/mosaic-media/mosaic-platform/internal/adapters/filesystem"
	"github.com/mosaic-media/mosaic-platform/internal/platform/domain"
)

// bundleRedactedPlaceholder replaces any free-text detail a support bundle
// must not carry verbatim (MEG-015 §09 — "Support bundles should be fully
// anonymised while allowing program and Module identification").
const bundleRedactedPlaceholder = "[REDACTED FOR SUPPORT BUNDLE]"

// SupportBundle is the anonymised diagnostic export MEG-015 §09 requires:
// program and Module/component identification, without personal data or
// secrets. ProgramID/ProgramVersion and each ComponentHealth's identifiers
// and states are never redacted — that is exactly the "program and Module
// identification" the spec says a bundle must still allow.
type SupportBundle struct {
	GeneratedAt    time.Time                `json:"generated_at"`
	ProgramID      string                   `json:"program_id"`
	ProgramVersion string                   `json:"program_version"`
	Components     []domain.ComponentHealth `json:"components"`
}

// BuildSupportBundle assembles an anonymised SupportBundle from a health
// snapshot (e.g. from Registry.Snapshot). Support bundles are stricter than
// local logs (MEG-015 §09): any ComponentHealth not explicitly classed
// RedactionNone has its free-text DegradedReason replaced outright, even
// though the same detail might be permitted (redacted-but-present) in a
// local log entry. Component identifiers, lifecycle, health state, last
// successful check, last failure category and dependency health are
// structural facts, not free text, so they are never redacted — that is
// the "program and Module identification" a bundle must still allow.
func BuildSupportBundle(programID, programVersion string, components []domain.ComponentHealth, generatedAt time.Time) SupportBundle {
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
	}
}

// WriteSupportBundle serializes bundle as indented JSON and writes it
// atomically to path (via internal/adapters/filesystem, so a crash never
// leaves a half-written bundle on disk). This is the "basic" mechanism
// MEG-015 §12 asks for: a single anonymised file, not a multi-file archive.
func WriteSupportBundle(path string, bundle SupportBundle) error {
	data, err := json.MarshalIndent(bundle, "", "  ")
	if err != nil {
		return err
	}
	return filesystem.WriteFileAtomic(path, data)
}
