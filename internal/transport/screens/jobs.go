// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The background-work surface (platform#13's no-user case), inside expert mode
// beside the telemetry screens.
//
// A queue with no surface fails silently: a dead-lettered job and an empty queue
// are indistinguishable from outside, and a runner gives up after N attempts
// precisely so a human looks. The screens are composed from the vocabulary that
// already exists — a TraceRow per job, a LogTable for its lines — so this costs
// no client release.

// jobsScreen lists the queue: what is waiting, what is running, what has been
// dead-lettered, and what has already run.
func (s *Service) jobsScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nav, navErr := s.settingsNav(ctx, caller)
	if navErr != nil {
		return nil, navErr
	}
	nav.selected = true

	filter := domain.JobFilter{Kind: stringParam(params, paramKind)}
	if status := stringParam(params, paramStatus); status != "" {
		filter.Status = domain.JobStatus(status)
	}

	res, err := s.content.ListJobs(ctx, app.ListJobsQuery{Caller: caller, Filter: filter})
	if err != nil {
		return nil, err
	}

	const lead = "Work the Platform does for itself, with no user behind it."
	body := make([]sdui.Node, 0, 4)
	if counts := jobCountRow(res.Jobs); counts != nil {
		body = append(body, counts.Build())
	}
	body = append(body, jobFilterRow(filter).Build())

	if len(res.Jobs) == 0 {
		body = append(body, ui.EmptyState(emptyIconCollections, "No jobs match that filter").Build())
		return settingsFrame(nav, sectionJobs, "Jobs", lead, body...), nil
	}

	rows := make([]ui.El, 0, len(res.Jobs))
	for _, j := range res.Jobs {
		rows = append(rows, ui.TraceRow(j.Kind,
			ui.Origin(shortID(string(j.ID))),
			ui.Summary(jobSummary(j)),
			ui.Value(string(j.Status)),
			ui.When(jobTone(j) != "", ui.StatusTone(jobTone(j))),
			ui.OnTap(ui.Navigate(screenJob, map[string]any{paramJobID: string(j.ID)}))))
	}
	body = append(body, ui.Stack("vertical", 2, rows...).Build())

	footer := strconv.Itoa(len(res.Jobs)) + " " + plural(len(res.Jobs), "job")
	body = append(body, ui.Stack("horizontal", 4,
		ui.Component("Text", ui.Prop("text", footer),
			ui.Prop("style", map[string]any{"variant": "xs", "color": "text-faint"})),
	).Build())

	return settingsFrame(nav, sectionJobs, "Jobs", lead, body...), nil
}

// jobScreen is one job: what it is, how each attempt went, and what it said
// about itself.
func (s *Service) jobScreen(ctx context.Context, caller v1.Caller, params map[string]any) (sdui.Node, error) {
	nav, navErr := s.settingsNav(ctx, caller)
	if navErr != nil {
		return nil, navErr
	}
	nav.selected = true

	id := domain.JobID(stringParam(params, paramJobID))
	res, err := s.content.GetJob(ctx, app.GetJobQuery{Caller: caller, JobID: id})
	if err != nil {
		return nil, err
	}

	const lead = "Every attempt at one job, and what it recorded while it ran."
	body := make([]sdui.Node, 0, 3)

	facts := []ui.El{
		ui.StatCard("Status", string(res.Job.Status)),
		ui.StatCard("Attempts", fmt.Sprintf("%d / %d", res.Job.Attempt, res.Job.MaxAttempts)),
	}
	if !res.Job.ScheduledAt.IsZero() {
		facts = append(facts, ui.StatCard("Runnable", res.Job.ScheduledAt.Format("15:04:05")))
	}
	if !res.Job.FinishedAt.IsZero() {
		facts = append(facts, ui.StatCard("Finished", res.Job.FinishedAt.Format("15:04:05")))
	}
	body = append(body, ui.Grid(ui.MinColumnWidth(130), ui.Gap(3), ui.Group(facts...)).Build())

	// The last error is stated in full rather than clamped into a row. It is
	// the one string somebody opened this screen to read, and it originates
	// outside the Platform often enough — an upstream's message, a module's —
	// that it is rendered as text and never as anything interpreted.
	if res.Job.LastError != "" {
		body = append(body, ui.Banner(res.Job.LastError, ui.ToneDanger).Build())
	}

	if len(res.Attempts) > 0 {
		rows := make([]ui.El, 0, len(res.Attempts))
		for _, a := range res.Attempts {
			summary := a.StartedAt.Format("15:04:05")
			if a.Error != "" {
				summary += " · " + a.Error
			}
			rows = append(rows, ui.TraceRow("Attempt "+strconv.Itoa(a.Attempt),
				ui.Origin(shortID(a.Runner)),
				ui.Summary(summary),
				ui.Value(formatDuration(a.Duration)),
				ui.StatusTone(attemptTone(a))))
		}
		body = append(body, ui.DetailPanel(res.Job.Kind,
			ui.Origin("job="+shortID(string(res.Job.ID))),
			ui.Summary(strconv.Itoa(len(res.Attempts))+" "+plural(len(res.Attempts), "attempt")),
			ui.Stack("vertical", 2, rows...),
		).Build())
	}

	if len(res.Logs) > 0 {
		rows := make([]any, 0, len(res.Logs))
		for _, l := range res.Logs {
			row := map[string]any{
				"time":    l.LoggedAt.Format("15:04:05"),
				"level":   l.Level,
				"tone":    logColor(l.Level),
				"service": res.Job.Kind,
				"message": l.Message,
			}
			// The trace of the run that wrote the line, and a way into its
			// waterfall — the same move the log viewer makes, and the one that
			// turns "it failed" into "here is where the time went and what the
			// code was saying". A run whose trace has since been dropped by
			// retention simply has no link.
			if l.Trace != "" {
				row["trace"] = shortID(l.Trace)
				row["action"] = ui.Navigate(screenTrace, map[string]any{paramTrace: l.Trace})
			}
			rows = append(rows, row)
		}
		body = append(body, ui.DetailPanel("What it recorded",
			ui.Summary(strconv.Itoa(len(res.Logs))+" "+plural(len(res.Logs), "line")),
			ui.LogTable(ui.Rows(rows)),
		).Build())
	}

	return settingsFrame(nav, sectionJobs, res.Job.Kind, lead, body...), nil
}

// jobSummary is the one line a queue row carries: where this job is, and when
// it next moves.
func jobSummary(j domain.Job) string {
	parts := []string{fmt.Sprintf("attempt %d of %d", j.Attempt, j.MaxAttempts)}
	switch j.Status {
	case domain.JobPending:
		if !j.ScheduledAt.IsZero() {
			parts = append(parts, "runnable "+j.ScheduledAt.Format("15:04:05"))
		}
	case domain.JobRunning:
		if j.LeasedBy != "" {
			parts = append(parts, "leased by "+shortID(j.LeasedBy))
		}
	default:
		if !j.FinishedAt.IsZero() {
			parts = append(parts, "finished "+j.FinishedAt.Format("15:04:05"))
		}
	}
	if j.LastError != "" {
		parts = append(parts, j.LastError)
	}
	return strings.Join(parts, " · ")
}

// jobTone colours a queue row by outcome. Empty for the states that are simply
// the queue working — a coloured dot on every row says nothing.
func jobTone(j domain.Job) string {
	switch {
	case j.Status == domain.JobDead:
		return "danger"
	case j.Status == domain.JobPending && j.Attempt > 0:
		// Pending with an attempt spent is a retry waiting out its backoff,
		// which is a different fact from pending-and-never-tried and the one
		// worth noticing.
		return "warning"
	case j.Status == domain.JobRunning:
		return "accent"
	default:
		return ""
	}
}

func attemptTone(a domain.JobAttempt) string {
	switch a.Status {
	case domain.JobAttemptFailed:
		return "danger"
	case domain.JobAttemptRunning:
		return "warning"
	default:
		return "accent"
	}
}

// jobCountRow is the run of chips over what came back — the same "count what
// is on screen, never claim a total the screen cannot see" rule the log level
// chips follow.
func jobCountRow(jobs []domain.Job) *ui.Element {
	counts := map[domain.JobStatus]int{}
	for _, j := range jobs {
		counts[j.Status]++
	}
	chips := make([]ui.El, 0, 4)
	for _, st := range []domain.JobStatus{domain.JobPending, domain.JobRunning, domain.JobSucceeded, domain.JobDead} {
		if counts[st] == 0 {
			continue
		}
		chips = append(chips, ui.StatCard(strings.ToUpper(string(st)), strconv.Itoa(counts[st])))
	}
	if len(chips) == 0 {
		return nil
	}
	return ui.Grid(ui.MinColumnWidth(120), ui.Gap(3), ui.Group(chips...))
}

// jobFilterRow offers the status filters as navigations back into this screen.
func jobFilterRow(f domain.JobFilter) *ui.Element {
	buttons := []ui.El{
		ui.Button("All", chipStyle(f.Status == ""), ui.OnTap(ui.Navigate(screenJobs, nil))),
	}
	for _, st := range []domain.JobStatus{domain.JobPending, domain.JobRunning, domain.JobSucceeded, domain.JobDead} {
		buttons = append(buttons, ui.Button(string(st), chipStyle(f.Status == st),
			ui.OnTap(ui.Navigate(screenJobs, map[string]any{paramStatus: string(st)}))))
	}
	return ui.Stack("horizontal", 2, ui.Wrap(true), ui.Group(buttons...))
}

// chipStyle is the on/off pair the filter rows use.
func chipStyle(on bool) string {
	if on {
		return "chipOn"
	}
	return "chip"
}
