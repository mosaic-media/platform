// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strings"
	"testing"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The background-work surface (platform#13). "Visible in expert mode" is the exit
// criterion, so what is tested is that it is visible to a caller who may see
// it, invisible to one who may not, and that a dead-letter reads as a failure
// rather than as a row like any other.

var jobsNow = time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC)

func jobFixtures() []domain.Job {
	return []domain.Job{
		{
			ID: "job-dead", Kind: "telemetry.retention", Status: domain.JobDead,
			Attempt: 5, MaxAttempts: 5, CreatedAt: jobsNow, FinishedAt: jobsNow,
			LastError: "the database went away",
		},
		{
			ID: "job-waiting", Kind: "telemetry.retention", Status: domain.JobPending,
			Attempt: 2, MaxAttempts: 5, CreatedAt: jobsNow, ScheduledAt: jobsNow.Add(time.Minute),
			LastError: "a transient failure",
		},
		{
			ID: "job-done", Kind: "telemetry.retention", Status: domain.JobSucceeded,
			Attempt: 1, MaxAttempts: 5, CreatedAt: jobsNow, FinishedAt: jobsNow,
		},
	}
}

// TestJobsNavRowIsHiddenWithoutTheGrant pins that job.read and telemetry.read
// are separate gates rather than one.
func TestJobsNavRowIsHiddenWithoutTheGrant(t *testing.T) {
	// Expert mode on, telemetry.read held, job.read not: the Diagnostics group
	// exists and the Jobs row inside it does not.
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, canReadJobs: false, expertModeOn: true,
	}
	svc := &Service{content: fake}

	rendered := nodeText(render(t, svc, "settings", nil))
	if !strings.Contains(rendered, "Traces") {
		t.Fatalf("the diagnostics group is missing entirely: %s", rendered)
	}
	if strings.Contains(rendered, "Jobs") {
		t.Fatalf("a caller without job.read was offered the jobs surface: %s", rendered)
	}
}

func TestJobsNavRowAppearsWithTheGrant(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, canReadJobs: true, expertModeOn: true,
	}
	svc := &Service{content: fake}

	if rendered := nodeText(render(t, svc, "settings", nil)); !strings.Contains(rendered, "Jobs") {
		t.Fatalf("a caller holding job.read was not offered the jobs surface: %s", rendered)
	}
}

// TestExpertModeIsOfferedForJobReadAlone pins that a caller who holds job.read
// and NOT telemetry.read still gets the expert-mode control, because there is
// something behind it for them. Without this the switch would be drawn from one
// permission and gate two.
func TestExpertModeIsOfferedForJobReadAlone(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: false, canReadJobs: true, expertModeOn: true,
	}
	svc := &Service{content: fake}

	rendered := nodeText(render(t, svc, "settings", nil))
	if !strings.Contains(rendered, "Expert mode") {
		t.Fatalf("job.read alone did not draw the expert-mode control: %s", rendered)
	}
	if !strings.Contains(rendered, "Jobs") {
		t.Fatalf("job.read alone did not draw the Jobs row: %s", rendered)
	}
	if strings.Contains(rendered, "Traces") {
		t.Fatalf("job.read alone drew the telemetry rows: %s", rendered)
	}
}

func TestJobsScreenListsTheQueueAndLinksIntoEachJob(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, canReadJobs: true,
		expertModeOn: true, jobs: jobFixtures(),
	}
	svc := &Service{content: fake}

	node := render(t, svc, "jobs", nil)
	var rows []sdui.Node
	findAll(node, "TraceRow", &rows)
	if len(rows) != 3 {
		t.Fatalf("drew %d job rows, want 3: %s", len(rows), nodeText(node))
	}

	// The dead-letter is the row somebody opened this to find, so it is toned
	// as a failure rather than as one entry among three.
	var dead sdui.Node
	for _, r := range rows {
		if strings.Contains(prop(r, "summary").(string), "the database went away") {
			dead = r
		}
	}
	if dead == nil {
		t.Fatalf("the dead-lettered job is not on the screen: %s", nodeText(node))
	}
	if got := prop(dead, "tone"); got != "danger" {
		t.Fatalf("the dead-lettered row is toned %v, want danger", got)
	}
	if got := prop(dead, "value"); got != string(domain.JobDead) {
		t.Fatalf("the dead-lettered row reads %v, want %s", got, domain.JobDead)
	}

	// Each row navigates into the job, which is where the attempts and the
	// lines are — the list says what happened, the detail says why.
	act, _ := prop(dead, "action").(map[string]any)
	if act["kind"] != sdui.KindNavigate {
		t.Fatalf("a job row emits %+v, want a Navigate", act)
	}
	if got := mapAt(act, "params")["jobId"]; got != "job-dead" {
		t.Fatalf("the row navigates with jobId=%v, want job-dead", got)
	}
}

// TestARetryWaitingOnItsBackoffIsMarkedAsSuch pins that a retry waiting out its
// backoff is not the same state as a job that has never run, and the screen
// must not present it as one.
func TestARetryWaitingOnItsBackoffIsMarkedAsSuch(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, canReadJobs: true,
		expertModeOn: true, jobs: jobFixtures(),
	}
	svc := &Service{content: fake}

	node := render(t, svc, "jobs", map[string]any{"status": string(domain.JobPending)})
	var rows []sdui.Node
	findAll(node, "TraceRow", &rows)
	if len(rows) != 1 {
		t.Fatalf("the status filter drew %d rows, want 1", len(rows))
	}
	if got := prop(rows[0], "tone"); got != "warning" {
		t.Fatalf("a waiting retry is toned %v, want warning", got)
	}
	if got := fake.gotJobFilter.Status; got != domain.JobPending {
		t.Fatalf("the screen asked for status %q, want %q", got, domain.JobPending)
	}
}

func TestJobScreenShowsEveryAttemptAndWhatTheJobRecorded(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, canReadJobs: true,
		expertModeOn: true, jobs: jobFixtures(),
		jobAttempts: []domain.JobAttempt{
			{JobID: "job-dead", Attempt: 1, StartedAt: jobsNow, Duration: 2 * time.Second,
				Status: domain.JobAttemptFailed, Error: "the database went away", Runner: "boot-1"},
			{JobID: "job-dead", Attempt: 2, StartedAt: jobsNow, Duration: time.Second,
				Status: domain.JobAttemptFailed, Error: "the database went away", Runner: "boot-1"},
		},
		jobLogs: []domain.JobLog{
			{JobID: "job-dead", LoggedAt: jobsNow, Level: "warn", Message: "attempt 1 failed — retrying in 30s",
				Trace: "0123456789abcdef0123456789abcdef"},
			// The last line has no trace: retention dropped the run that wrote
			// it, which is the ordinary state for a job that failed a fortnight
			// ago and the reason the lines are stored beside the job at all.
			{JobID: "job-dead", LoggedAt: jobsNow, Level: "error", Message: "dead-lettered after 5 attempts"},
		},
	}
	svc := &Service{content: fake}

	node := render(t, svc, "job", map[string]any{"jobId": "job-dead"})
	if fake.gotJobID != "job-dead" {
		t.Fatalf("the screen asked for job %q", fake.gotJobID)
	}

	var rows []sdui.Node
	findAll(node, "TraceRow", &rows)
	if len(rows) != 2 {
		t.Fatalf("drew %d attempt rows, want 2: %s", len(rows), nodeText(node))
	}
	if got := prop(rows[0], "tone"); got != "danger" {
		t.Fatalf("a failed attempt is toned %v, want danger", got)
	}

	// The lines are on the screen. They are stored beside the job precisely so
	// they outlive telemetry retention, so a detail screen that did not show
	// them would waste the only durable record of a failure.
	table, ok := find(node, "LogTable")
	if !ok {
		t.Fatalf("the job's own lines are not shown: %s", nodeText(node))
	}
	lines, _ := prop(table, "rows").([]any)
	if len(lines) != 2 {
		t.Fatalf("drew %d log lines, want 2", len(lines))
	}

	// A line that still has its trace links into the waterfall — the move that
	// turns "it failed" into "here is where the time went". One that has lost
	// it to retention links nowhere rather than to a trace that is not there.
	traced, _ := lines[0].(map[string]any)
	act, _ := traced["action"].(map[string]any)
	if act["kind"] != sdui.KindNavigate {
		t.Fatalf("a traced job line emits %+v, want a Navigate", act)
	}
	if got := mapAt(act, "params")["trace"]; got != "0123456789abcdef0123456789abcdef" {
		t.Fatalf("the line navigates to trace %v, want the full id", got)
	}
	untraced, _ := lines[1].(map[string]any)
	if _, ok := untraced["action"]; ok {
		t.Fatal("a line whose trace has been dropped still offered a link to it")
	}
	if rendered := nodeText(node); !strings.Contains(rendered, "dead-lettered after 5 attempts") {
		t.Fatalf("the dead-letter line is not rendered: %s", rendered)
	}
	// And the failure that ended it is stated in full rather than clamped into
	// a row, which is the one string somebody opened this screen to read.
	if !strings.Contains(nodeText(node), "the database went away") {
		t.Fatalf("the last error is not on the screen: %s", nodeText(node))
	}
}

func TestJobsScreenEmptyStateSaysNothingMatched(t *testing.T) {
	fake := &fakeQueries{
		settingsUI: minimalSettingsUI(), canReadTelemetry: true, canReadJobs: true, expertModeOn: true,
	}
	svc := &Service{content: fake}

	node := render(t, svc, "jobs", nil)
	if _, ok := find(node, "EmptyState"); !ok {
		t.Fatalf("an empty queue drew no empty state: %s", nodeText(node))
	}
}
