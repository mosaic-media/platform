// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The maintenance pass (platform#60, roadmap M2.3).
//
// It evaluates each enabled rule and reconciles: materialise what is missing,
// and for what is already there, re-merge artwork and top up Parts through the
// enrichment pass platform#46 made idempotent.
//
// It adds and never removes. A title that has left a catalog stays in the
// library; do not add a deletion path here.
//
// It acts as the system principal (platform#13) whoever triggered it, so a
// maintenance write does not fail because the administrator who wrote the rule
// was suspended. A manual run is authorised against the person who pressed the
// button and then acts as the install.
//
// It is bounded. Rules turn upstream load from bursty and human-triggered into
// continuous, and a refresh is not free: the module re-reads the title's detail
// before it discovers it already has it. The budget is per run and is
// configuration.
//
// It is best-effort per item: one failure logs and continues, so an unreachable
// source cannot stop the run.
//
// What it does not do: extend a tree that already exists. Every module dedups
// before writing and returns AlreadyKnown on a title it has already
// materialised, so a series that gains a season is refreshed — artwork, Parts —
// and does not grow the new season. Closing that needs a refresh path through
// the module, which is an SDK change.

// Config fields governing the pass, matching config.PlatformSchema. Named as
// constants rather than spelled at the read site because a misspelling would be
// a configured value that silently never takes effect.
const (
	fieldLibraryMaintenanceInterval = "library.maintenance.interval_hours"
	fieldLibraryMaintenanceBudget   = "library.maintenance.items_per_run"
)

// LibraryMaintenanceSettings is how often the pass runs and how much it may do.
type LibraryMaintenanceSettings struct {
	// Interval is how often the schedule fires.
	Interval time.Duration
	// Budget is how many items one run may attempt across every rule —
	// materialised and refreshed alike, because a refresh costs the source a
	// round trip too.
	Budget int
}

// DefaultLibraryMaintenance is what applies when the Active configuration says
// nothing, which is the normal state: there is no admin surface for drafting
// configuration yet.
//
// Six-hourly rather than hourly: a catalog is curated by people and changes on
// the order of days, and the cost of asking is paid to somebody else's API on a
// credential a whole household may share (architecture#4).
var DefaultLibraryMaintenance = LibraryMaintenanceSettings{
	Interval: 6 * time.Hour,
	Budget:   200,
}

// LibraryMaintenance reads the pass's settings from the Active configuration,
// falling back to the defaults for anything unset or unusable.
//
// It never fails, like retention's reader: this governs a background sweep, and
// a typo in configuration must not become a Platform that will not maintain its
// library.
func (s *Service) LibraryMaintenance(ctx context.Context) LibraryMaintenanceSettings {
	out := DefaultLibraryMaintenance
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
	if d, ok := durationField(payload, fieldLibraryMaintenanceInterval, time.Hour); ok {
		out.Interval = d
	}
	if n, ok := countField(payload, fieldLibraryMaintenanceBudget); ok {
		out.Budget = n
	}
	return out
}

// countField reads a positive whole-number config field. It sits beside
// durationField rather than reusing it with a unit of one nanosecond, because a
// budget is a count of items rather than a length of time. Zero and negatives
// are rejected: a budget of zero is a sweep that does nothing while reporting
// that it ran.
func countField(payload map[string]any, key string) (int, bool) {
	raw, ok := payload[key]
	if !ok {
		return 0, false
	}
	var n float64
	switch v := raw.(type) {
	case float64:
		n = v
	case string:
		if err := json.Unmarshal([]byte(v), &n); err != nil {
			return 0, false
		}
	default:
		return 0, false
	}
	if n < 1 {
		return 0, false
	}
	return int(n), true
}

// RunLibraryMaintenanceCommand evaluates every enabled rule and reconciles.
type RunLibraryMaintenanceCommand struct {
	// Caller is who triggered the pass — the system principal on a schedule, an
	// administrator on a manual run. It is not who the pass acts as.
	Caller v1.Caller
	// JobID is the queued job this run belongs to, when there is one. The pass
	// writes a line per rule beside it. Empty for a run with no job behind it,
	// which writes no job lines and is otherwise identical.
	JobID domain.JobID
}

// RunLibraryMaintenanceResult is the whole pass in the four numbers platform#60
// asks every run to record, plus what it cost.
type RunLibraryMaintenanceResult struct {
	// Rules is how many enabled rules were evaluated.
	Rules int
	// Matched, Created, Refreshed, Skipped and Failed sum the per-rule accounts.
	Matched   int
	Created   int
	Refreshed int
	Skipped   int
	Failed    int
	// Budget is how many items the run was allowed to attempt, and Exhausted
	// says it ran out. A run that stopped on its budget has left work for the
	// next one — a normal outcome, but it must stay visible, or a rule that
	// never finishes filling looks like a rule that does not work.
	Budget    int
	Exhausted bool
}

// RunLibraryMaintenance evaluates the enabled rules and reconciles the library
// against them.
//
// The command boundary authorises the trigger — content.import, the authority
// to cause materialisation at all — and everything beneath it acts as the
// system principal, so a person may press the button and the nodes still belong
// to the install.
func (s *Service) RunLibraryMaintenance(ctx context.Context, cmd RunLibraryMaintenanceCommand) (RunLibraryMaintenanceResult, error) {
	if cmd.Caller.Session == "" {
		return RunLibraryMaintenanceResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	if _, err := s.enter(ctx, cmd.Caller, ActionContentImport, policy.Resource{Type: "content"}); err != nil {
		return RunLibraryMaintenanceResult{}, err
	}
	if s.libraryRules == nil {
		return RunLibraryMaintenanceResult{}, contracts.NewError(contracts.Unavailable, "this Platform stores no library rules")
	}

	settings := s.LibraryMaintenance(ctx)
	result := RunLibraryMaintenanceResult{Budget: settings.Budget}

	rules, err := s.libraryRules.List(ctx, domain.LibraryRuleFilter{EnabledOnly: true})
	if err != nil {
		return RunLibraryMaintenanceResult{}, err
	}

	// The principal every write beneath this runs as. Resolved once so the whole
	// pass is one principal, and so a manual run and a scheduled one produce
	// indistinguishable graphs.
	system := s.SystemCaller()
	az, err := s.enter(ctx, system, ActionContentImport, policy.Resource{Type: "content"})
	if err != nil {
		return RunLibraryMaintenanceResult{}, err
	}

	remaining := settings.Budget
	for _, rule := range rules {
		run := s.reconcileLibraryRule(ctx, az, system, rule, &remaining)

		result.Rules++
		result.Matched += run.Matched
		result.Created += run.Created
		result.Refreshed += run.Refreshed
		result.Skipped += run.Skipped
		result.Failed += run.Failed

		// Recorded per rule as the pass goes, not at the end, so a run that
		// dies halfway still leaves an account of the rules it did finish.
		if err := s.libraryRules.RecordRun(ctx, rule.ID, run); err != nil {
			telemetry.From(ctx).For("library").Warn("could not record the rule's run",
				telemetry.String("rule", rule.Name), telemetry.Err(err))
		}
		logLibraryRule(ctx, rule, run)
		s.appendJobLog(ctx, cmd.JobID, jobLogLevelFor(run), libraryRunLine(rule, run))

		if remaining <= 0 {
			result.Exhausted = true
			// Every rule after this one is untouched rather than half-done;
			// their last-run accounts stay as they were, and the next run
			// starts from the top with a fresh budget.
			break
		}
	}

	if result.Exhausted {
		s.appendJobLog(ctx, cmd.JobID, "warn", fmt.Sprintf(
			"the run's budget of %d items was spent before every rule was evaluated; the next run continues",
			settings.Budget))
	}
	telemetry.From(ctx).For("library").Info("library maintenance finished",
		telemetry.Int("rules", result.Rules),
		telemetry.Int("created", result.Created),
		telemetry.Int("refreshed", result.Refreshed),
		telemetry.Int("skipped", result.Skipped),
		telemetry.Int("failed", result.Failed),
		telemetry.Bool("budget_exhausted", result.Exhausted))
	return result, nil
}

// reconcileLibraryRule evaluates one rule and materialises what it matched,
// spending from the run's shared budget.
//
// It returns an account rather than an error: a rule that could not be
// evaluated at all records why in LibraryRuleRun.Error and the pass moves to the
// next one. One rule's source being down must not stop the others, just as one
// item's failure must not stop its rule.
func (s *Service) reconcileLibraryRule(ctx context.Context, az authorized, system v1.Caller, rule domain.LibraryRule, remaining *int) domain.LibraryRuleRun {
	run := domain.LibraryRuleRun{At: s.clock.Now()}

	matches, _, err := s.evaluateLibraryRule(ctx, az, system, rule)
	if err != nil {
		run.Error = err.Error()
		run.At = s.clock.Now()
		return run
	}
	run.Matched = len(matches)

	for _, match := range matches {
		if *remaining <= 0 {
			// Out of budget: everything left is counted as skipped rather than
			// dropped, so matched, created, refreshed, skipped and failed still
			// add up to Matched in the run log.
			run.Skipped++
			continue
		}
		if match.Ref.Provider == "" || match.Ref.NativeID == "" || match.Ref.NativeType == "" {
			// A source that described an item too thinly to materialise. Not a
			// failure — nothing was attempted — and not silence either.
			run.Skipped++
			continue
		}

		*remaining--
		// The import is the whole reconcile for one item: the module creates the
		// tree if it is missing and reports AlreadyKnown if it is not, and the
		// Platform's enrichment then re-merges artwork and fills Parts for items
		// that have none (platform#46). Both paths are wanted, which is why a
		// title already in the library is imported rather than skipped.
		res, err := s.ImportContent(ctx, ImportContentCommand{Caller: system, Ref: match.Ref})
		switch {
		case err != nil:
			run.Failed++
			telemetry.From(ctx).For("library").Warn("a library rule could not materialise an item",
				telemetry.String("rule", rule.Name),
				telemetry.Sensitive("title", match.Title),
				telemetry.Err(err))
		case res.AlreadyKnown:
			run.Refreshed++
		default:
			run.Created++
		}
	}

	run.At = s.clock.Now()
	return run
}

// libraryRunLine is the sentence a run writes beside the job — what an
// administrator reads when asking why something is in the library.
func libraryRunLine(rule domain.LibraryRule, run domain.LibraryRuleRun) string {
	if run.Error != "" {
		return fmt.Sprintf("%s (%s) could not be evaluated: %s",
			rule.Name, describeLibraryRule(rule), run.Error)
	}
	return fmt.Sprintf("%s (%s): %d matched, %d created, %d refreshed, %d skipped, %d failed",
		rule.Name, describeLibraryRule(rule),
		run.Matched, run.Created, run.Refreshed, run.Skipped, run.Failed)
}

// jobLogLevelFor grades a rule's outcome. A rule that could not be evaluated is
// an error because somebody has to fix it; items failing inside a rule that ran
// is a warning, which is what best-effort looks like from outside.
func jobLogLevelFor(run domain.LibraryRuleRun) string {
	switch {
	case run.Error != "":
		return "error"
	case run.Failed > 0:
		return "warn"
	default:
		return "info"
	}
}

// appendJobLog writes one line beside the job this pass belongs to.
//
// Best-effort and quiet: a run that did its work is not undone because the note
// about it could not be stored, the same rule the runner's own logging follows.
// A pass with no job behind it — a test, or a manual run — writes nothing here
// and records everything else exactly as it would.
func (s *Service) appendJobLog(ctx context.Context, jobID domain.JobID, level, message string) {
	if jobID == "" || s.jobs == nil {
		return
	}
	tc, _ := telemetry.TraceFrom(ctx)
	err := s.jobs.AppendLog(ctx, domain.JobLog{
		ID:       domain.JobLogID(s.ids.NewID()),
		JobID:    jobID,
		LoggedAt: s.clock.Now(),
		Level:    level,
		Message:  message,
		Trace:    tc.TraceIDString(),
	})
	if err != nil {
		telemetry.From(ctx).For("library").Warn("appending the run's job log failed", telemetry.Err(err))
	}
}
