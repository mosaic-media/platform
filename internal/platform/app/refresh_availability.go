// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The watch-availability refresh (roadmap M2.5).
//
// Availability churns monthly, so grouping by streaming service is only correct
// while something re-asks: a group saying "on Netflix" about a title that left
// in March is worse than an absent group, because a user can see a feature that
// is missing and cannot see one that is lying. This pass is for the second
// visit; the first is free, because the import already asked.
//
// It is not the library maintenance pass. That one walks the rules and
// re-imports what they match, so a title nobody wrote a rule for — anything
// added by hand from search — is never revisited, and it works through the
// module, which re-reads a whole title's detail. This walks the library, oldest
// answer first, so every work with a stored answer is eventually re-asked and
// none starves.

// Config fields governing the pass. Named as constants rather than spelled at
// the read site because a misspelling is a configured value that silently never
// takes effect.
const (
	fieldAvailabilityInterval = "library.availability.interval_hours"
	fieldAvailabilityBudget   = "library.availability.items_per_run"
)

// AvailabilitySettings is how often the refresh runs and how much it may do.
type AvailabilitySettings struct {
	// Interval is how often the schedule fires.
	Interval time.Duration
	// Budget is how many works one run may re-ask about. Bounded rather than
	// "all of them" for the reason the maintenance pass is: this turns upstream
	// load from human-triggered into continuous, and the credential paying for it
	// may be one a whole household shares (architecture#4).
	Budget int
}

// DefaultAvailability is what applies when the Active configuration says
// nothing, which is the normal state.
//
// Daily, chosen against what it refreshes: streaming catalogues move on the
// order of a month, so asking once a day is already an order of magnitude more
// often than the fact changes. The budget decides how long a full sweep takes —
// at 200 a day a library of two thousand works is ten days end to end, well
// inside the churn it is tracking.
var DefaultAvailability = AvailabilitySettings{
	Interval: 24 * time.Hour,
	Budget:   200,
}

// Availability reads the pass's schedule out of the Active configuration,
// falling back to the defaults field by field.
//
// It never fails, like the maintenance pass's reader: this governs a background
// sweep, and a typo in configuration must not become a Platform that never
// refreshes its availability — a refresh that silently stopped is how a group
// goes back to lying.
func (s *Service) Availability(ctx context.Context) AvailabilitySettings {
	out := DefaultAvailability
	if s.configStore == nil {
		return out
	}
	version, err := s.configStore.FindActive(ctx)
	if err != nil || len(version.Payload) == 0 {
		return out
	}
	var payload map[string]any
	if err := json.Unmarshal(version.Payload, &payload); err != nil {
		return out
	}
	if d, ok := durationField(payload, fieldAvailabilityInterval, time.Hour); ok {
		out.Interval = d
	}
	if n, ok := countField(payload, fieldAvailabilityBudget); ok {
		out.Budget = n
	}
	return out
}

// RefreshAvailabilityCommand asks for one bounded pass.
type RefreshAvailabilityCommand struct {
	Caller v1.Caller
	// Budget overrides the configured one for this run. Zero takes the
	// configured value.
	Budget int
}

// RefreshAvailabilityResult accounts for what a run did. Every work a run takes
// must land in exactly one of these counters, so they sum to the budget spent.
type RefreshAvailabilityResult struct {
	// Checked is how many works were re-asked about and had their answer
	// stored.
	Checked int
	// Skipped is how many were taken and could not be re-asked — a stored
	// document with no ref in it, or a provider that is no longer installed.
	Skipped int
	// Failed is how many were asked and errored.
	Failed int
}

// RefreshAvailability re-asks the metadata provider about the works whose
// answers are oldest, and stores what it is told.
//
// It acts as the system principal (platform#13), like the maintenance pass: a
// refresh must not fail because the person who last signed in has since been
// suspended, and nothing here is anybody's decision.
func (s *Service) RefreshAvailability(ctx context.Context, cmd RefreshAvailabilityCommand) (RefreshAvailabilityResult, error) {
	if cmd.Caller.Session == "" {
		return RefreshAvailabilityResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	// The outer boundary authorises whoever asked; every read and write
	// beneath it is the install's.
	if _, err := s.enter(ctx, cmd.Caller, ActionContentImport, policy.Resource{Type: "content"}); err != nil {
		return RefreshAvailabilityResult{}, err
	}
	if s.watchAvailability == nil || s.nodeMetadata == nil {
		// A Service built without the stores is a test service and must not
		// silently start writing, the same guard enrichMetadata takes.
		return RefreshAvailabilityResult{}, nil
	}

	budget := cmd.Budget
	if budget <= 0 {
		budget = s.Availability(ctx).Budget
	}

	stale, err := s.watchAvailability.ListStale(ctx, budget)
	if err != nil {
		return RefreshAvailabilityResult{}, err
	}

	system := s.SystemCaller()
	var out RefreshAvailabilityResult
	for _, nodeID := range stale {
		ref, ok := s.storedRefFor(ctx, nodeID)
		if !ok {
			// Nothing to re-ask with — a stored document predating the ref being
			// carried, or a provider whose answer never named itself. The row is
			// left exactly as it was, with its old timestamp, so it stays at the
			// head of the queue rather than being dropped from the rotation.
			out.Skipped++
			continue
		}
		// The whole enrichment, not just the availability: it is the same fetch,
		// so re-asking for one and discarding the other pays a provider round
		// trip to keep a document stale.
		if err := s.enrichMetadataErr(ctx, system, ref, nodeID); err != nil {
			out.Failed++
			telemetry.From(ctx).For("library").Warn("availability refresh could not re-ask a provider",
				telemetry.String("node_id", string(nodeID)),
				telemetry.String("module", ref.Provider),
				telemetry.Err(err))
			continue
		}
		out.Checked++
	}
	return out, nil
}

// storedRefFor recovers the ref a work was last described by.
//
// A materialised node cannot be turned back into a provider-bearing ref: it
// records the external scheme and id but not the module that served it nor that
// module's native type vocabulary — the problem platform#45 named. platform#62
// stores the provider's whole answer including ContentMetadata.Ref, so a work
// that has ever been enriched carries the ref it was enriched by, written by the
// provider rather than reconstructed here. A work that has never been enriched
// has no ref and is skipped.
func (s *Service) storedRefFor(ctx context.Context, nodeID v1.NodeID) (v1.ContentRef, bool) {
	stored, err := s.nodeMetadata.Get(ctx, nodeID)
	if err != nil {
		return v1.ContentRef{}, false
	}
	var meta v1.ContentMetadata
	if err := json.Unmarshal(stored.Document, &meta); err != nil {
		return v1.ContentRef{}, false
	}
	if meta.Ref.Provider == "" || meta.Ref.NativeID == "" {
		return v1.ContentRef{}, false
	}
	return meta.Ref, true
}
