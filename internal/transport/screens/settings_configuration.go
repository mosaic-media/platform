// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Settings › Configuration — the client path to the activation state machine
// (ADR 0011, roadmap M4.4).
//
// The five services behind it — draft, validate, activate, read the active
// version, read the pending one — were complete, tested and undrivable: the
// reload-class model classified a change correctly and nobody could ask for
// one. That is the register entry this panel discharges.
//
// **The three write services are one control.** Draft, validate and activate
// are the machinery of the model, not a workflow a person performs: somebody
// changing how often the library pass runs is not drafting a version. Exposing
// them as three controls would make an operator drive an implementation in
// order and leave a half-finished draft behind every time they changed their
// mind. What survives from the three into the surface is the thing only they
// can say — whether the change took effect, or is waiting for something bigger
// than this process.

const (
	// sectionConfiguration is the settings section this panel fills.
	sectionConfiguration = "configuration"

	applyConfigurationAction = "applyConfiguration"
)

// configurableField is one setting an administrator may change from here, with
// the sentence that says what the Platform does when it is unset.
type configurableField struct {
	name  string
	label string
	help  string
	// unit is what the number means, appended to a value when it is shown.
	// Empty for a field whose value speaks for itself.
	unit string
}

// The fields this panel offers, and the test that decided the list:
// **something in the Platform must actually read the field.**
//
// That test excludes two the schema declares. `runtime.log_level` is the
// schema's own comment's "canonical hot-reload example" and nothing reads it;
// `runtime.environment` is the same. Offering either would be a control that
// saves a value, reports that it applied, and changes nothing — an affordance
// with nothing behind it, which is the exact failure ADR 0036 exists to
// prevent and the one hardest to notice, because the write really did succeed.
//
// It also excludes two the Platform reads and this panel must not offer.
// `composition.modules` is Generation-class and the Generation activation it
// needs is not built, so a value saved here would wait for an escalation that
// cannot happen. `storage.postgres.dsn` and its password are Recovery-class,
// secret, and the two values that lock an operator out of their own install if
// a screen gets them wrong.
//
// They are grouped by what they are for rather than by reload class: the class
// is a consequence read off the row, not the way somebody thinks about their
// own server.
var configurableFields = []configurableField{
	{name: "telemetry.retention.logs_days", label: "Keep logs for", unit: "days",
		help: "How long a log line is kept before the retention sweep discards it."},
	{name: "telemetry.retention.traces_hours", label: "Keep traces for", unit: "hours",
		help: "Traces are far larger than logs and are kept for hours rather than days."},
	{name: "telemetry.retention.metrics_days", label: "Keep metrics for", unit: "days",
		help: "How long the recorded measurements are kept."},
	{name: "telemetry.retention.audit_days", label: "Keep the audit log for", unit: "days",
		help: "This one has a floor of 30 days that configuration cannot lower (ADR 0057). " +
			"A shorter value is accepted and the floor still applies."},
	{name: "library.maintenance.interval_hours", label: "Library pass, every", unit: "hours",
		help: "How often the pass that keeps rule-built collections current runs."},
	{name: "library.maintenance.items_per_run", label: "Library pass budget", unit: "items",
		help: "How many items one run may touch, so a large library does not become a burst " +
			"of traffic to somebody else's API."},
	{name: "library.availability.interval_hours", label: "Availability refresh, every", unit: "hours",
		help: "How often the Platform re-asks which services a title streams on."},
	{name: "library.availability.items_per_run", label: "Availability budget", unit: "items",
		help: "How many titles one refresh may check. This decides how long a full sweep takes."},
}

// configurationPanel shows what this install is configured to do, what a change
// to each field would cost, and what is waiting for a restart.
func (s *Service) configurationPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel) (sdui.Node, error) {
	effective, err := s.effectiveConfiguration(ctx, caller)
	if err != nil {
		return nil, err
	}
	pending, err := s.content.GetPendingConfigVersion(ctx, app.GetPendingConfigVersionQuery{
		CallerSessionID: domain.SessionID(caller.Session),
	})
	if err != nil {
		return nil, err
	}

	schema := config.PlatformSchema()
	body := []sdui.Node{}

	// What is waiting, and what it is waiting for. This is the only place the
	// escalation is visible to a person: before it, a change that needed a
	// restart simply appeared not to have happened.
	if pending.Found {
		body = append(body, ui.Banner(
			pendingSentence(pending, s.now()), ui.ToneWarning).Build())
	}

	rows := make([]ui.El, 0, len(configurableFields))
	for _, field := range configurableFields {
		class, _ := schema.ReloadClassOf(field.name)
		rows = append(rows, ui.SettingsRow(field.label,
			ui.Summary(reloadClassSentence(class)),
			ui.Value(effective[field.name])))
	}
	body = append(body, ui.Section("What it is set to now",
		ui.Stack("vertical", 0, rows...)).Build())

	// One form, one submit, and the three services behind it.
	//
	// Every box is pre-filled with the value that applies, and an empty one
	// means "leave this alone" rather than "set it to empty" — so a person
	// changing one number is not silently re-stating the other seven.
	if s.content.CallerCan(ctx, caller, app.ActionConfigActivate, "config") {
		body = append(body, configurationForm(effective).Build())
	}

	return settingsFrame(nav, sectionConfiguration, "Configuration",
		"What this server is set to do. A change that needs a restart says so, and takes effect on the next one.",
		body...), nil
}

// configurationForm is the editable half: one typed box per field, and one
// submit that drives draft, validate and activate as a single act.
func configurationForm(effective map[string]string) *ui.Element {
	fields := make([]ui.El, 0, len(configurableFields))
	vars := make([]map[string]any, 0, len(configurableFields)+1)
	vars = append(vars, sdui.Var(fieldFormError, sdui.VarString, ""))
	for _, field := range configurableFields {
		vars = append(vars, sdui.Var(field.name, sdui.VarString, ""))
		fields = append(fields, ui.TextField(field.label,
			ui.Name(field.name),
			// The placeholder is the value that applies, so an empty box reads
			// as "this stays as it is" rather than as a value that was lost.
			ui.Placeholder(effective[field.name]),
			ui.Help(field.help),
			ui.InputType("number")))
	}
	return ui.Section("Change it",
		ui.Banner("Leave a box empty to leave that setting alone. Everything here is a whole number.",
			ui.ToneInfo),
		ui.State(
			ui.Vars(sdui.Vars(vars...)),
			ui.Form(
				ui.BindError(fieldFormError),
				ui.SubmitLabel("Apply"),
				ui.SubmitAction(ui.Submit(ui.Invoke(applyConfigurationAction, nil), "")),
				ui.Group(fields...))))
}

// effectiveConfiguration is what each field is actually worth right now.
//
// It asks the readers rather than formatting the stored payload, and the
// difference is not cosmetic: each reader falls back to its own default for an
// unset field, rejects an unusable value and falls back for that too, and the
// audit floor is applied after all of it. Rendering the payload would show a
// budget of 0 that the Platform is not using and an unset field as "not set"
// when a documented default is in force.
//
// It reads the Active version first purely as the authorisation gate — the
// value it returns is discarded. That call is the one that authorises
// `config.read`; the readers below take no caller, because they exist to serve
// the Platform's own background passes.
func (s *Service) effectiveConfiguration(ctx context.Context, caller v1.Caller) (map[string]string, error) {
	if _, err := s.content.GetActiveConfigVersion(ctx, app.GetActiveConfigVersionQuery{
		CallerSessionID: domain.SessionID(caller.Session),
	}); err != nil && contracts.CategoryOf(err) != contracts.NotFound {
		// NotFound is the ordinary state of a fresh install — no version has
		// ever been activated — and the defaults below are the honest answer
		// for it, so it is not a reason to refuse the panel.
		return nil, err
	}

	retention := s.content.TelemetryRetention(ctx)
	maintenance := s.content.LibraryMaintenance(ctx)
	availability := s.content.Availability(ctx)

	return map[string]string{
		"telemetry.retention.logs_days":       inDays(retention.Logs),
		"telemetry.retention.traces_hours":    inHours(retention.Spans),
		"telemetry.retention.metrics_days":    inDays(retention.Metric),
		"telemetry.retention.audit_days":      inDays(retention.Audit),
		"library.maintenance.interval_hours":  inHours(maintenance.Interval),
		"library.maintenance.items_per_run":   strconv.Itoa(maintenance.Budget),
		"library.availability.interval_hours": inHours(availability.Interval),
		"library.availability.items_per_run":  strconv.Itoa(availability.Budget),
	}, nil
}

func inDays(d time.Duration) string  { return strconv.Itoa(int(d.Hours() / 24)) }
func inHours(d time.Duration) string { return strconv.Itoa(int(d.Hours())) }

// pendingSentence says what is waiting, what it would change, and what will
// apply it — in terms a person can act on, since a restart is something they
// can cause.
//
// Naming the fields matters more here than anywhere else on the panel. A
// pending version is invisible from every other angle: the rows above show
// what applies *now*, so a change that is waiting looks exactly like a change
// that was never made.
func pendingSentence(pending app.GetPendingConfigVersionResult, now time.Time) string {
	var what string
	if changed := changedSummary(pending.Version.Payload); changed != "" {
		what = " It sets " + changed + "."
	}
	var when string
	if at := pending.Version.RequestedAt; at != nil {
		if ago := lastRunAgo(*at, now); ago != "" {
			when = " Asked for " + ago + "."
		}
	}
	switch pending.ReloadClass {
	case config.Restart:
		return "A change is waiting for the server to restart, and takes effect then." + what + when
	case config.Generation:
		return "A change is waiting for an upgrade to be activated." + what + when +
			" Activating a Generation is not built yet, so nothing can apply this one."
	case config.Recovery:
		return "A change can only be applied through the recovery flow." + what + when
	default:
		return "A change is waiting to be applied." + what + when
	}
}

// reloadClassSentence says what changing a field costs, in the terms of the
// person paying it rather than the vocabulary's.
func reloadClassSentence(class config.ReloadClass) string {
	switch class {
	case config.Hot:
		return "Takes effect straight away"
	case config.Restart:
		return "Takes effect when the server restarts"
	case config.Generation:
		return "Takes effect on the next upgrade"
	case config.Recovery:
		return "Can only be changed through recovery"
	}
	return string(class)
}

// payloadValues flattens a configuration payload into field/value strings.
//
// A payload that cannot be read yields no values rather than an error: this
// serves the diagnostic line below the form, whose job is to show what was
// stored, and an unreadable stored payload is itself the thing worth showing.
func payloadValues(payload []byte) map[string]string {
	values := map[string]string{}
	if len(payload) == 0 {
		return values
	}
	var raw map[string]any
	if err := json.Unmarshal(payload, &raw); err != nil {
		return values
	}
	for key, value := range raw {
		values[key] = fmt.Sprint(value)
	}
	return values
}

// changedSummary names the fields a pending version would change, for the
// sentence that says what is waiting.
func changedSummary(payload []byte) string {
	values := payloadValues(payload)
	labels := make([]string, 0, len(values))
	for _, field := range configurableFields {
		if v, ok := values[field.name]; ok {
			labels = append(labels, strings.ToLower(field.label)+" → "+v)
		}
	}
	return joinWords(labels)
}
