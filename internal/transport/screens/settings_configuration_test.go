// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
)

// Settings › Configuration (platform#7, roadmap M4.4).
//
// The properties guarded here are the ones that would fail quietly. A panel
// that renders is not a panel that is right about anything: the numbers on it
// have to be the numbers the Platform is actually using, and a change waiting
// for a restart has to be visible — because from every other angle it looks
// exactly like a change that was never made.

func configService(fake *fakeQueries) *Service {
	if fake.allow == nil {
		fake.allow = map[string]bool{}
	}
	for _, a := range app.AdministratorActions() {
		fake.allow[string(a)] = true
	}
	return &Service{content: fake, clock: func() time.Time {
		return time.Date(2026, 8, 8, 12, 0, 0, 0, time.UTC)
	}}
}

// **The load-bearing test on the read side.** A fresh install has activated no
// configuration version at all, and the honest thing to show is what the
// Platform is running with — which is each reader's own documented default, not
// "not set" and not zero. A panel that showed the stored payload would render
// nothing here while the Platform kept logs for a fortnight.
func TestTheConfigurationPanelShowsWhatIsInForceNotWhatIsStored(t *testing.T) {
	fake := &fakeQueries{
		activeConfigErr: contracts.NewError(contracts.NotFound, "no active configuration version"),
	}
	svc := configService(fake)

	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))

	// 14 days of logs and a 200-item maintenance budget are the defaults the
	// readers apply. Naming the numbers rather than the constants is the point:
	// this asserts the panel reports what runs, so it should fail if either
	// default moves without the screen being looked at.
	for _, want := range []string{"Keep logs for", "14", "Library pass budget", "200"} {
		if !strings.Contains(text, want) {
			t.Errorf("the panel does not report %q: %s", want, text)
		}
	}
	if strings.Contains(text, "not set") {
		t.Error("the panel reported a field as unset when a default is in force")
	}
}

// The audit floor (platform#35) is applied by the reader and not by configuration,
// so a panel formatting the stored payload would show whatever an operator
// asked for while the Platform kept 30 days. Reading through the service is
// what makes the floor visible.
func TestTheAuditFloorIsWhatThePanelReports(t *testing.T) {
	// What the reader answers once the floor has been applied — one day was
	// requested, thirty is in force.
	floored := app.DefaultTelemetryRetention
	floored.Audit = 30 * 24 * time.Hour
	fake := &fakeQueries{retention: &floored}
	svc := configService(fake)

	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))
	if !strings.Contains(text, "floor of 30 days") {
		t.Errorf("the panel does not say the audit floor cannot be lowered: %s", text)
	}
}

// **The load-bearing test on the escalation.** A Restart-class change is
// accepted, stored and does nothing until the process restarts. The rows above
// it show what applies *now*, so without this banner the panel is a screen that
// silently discards what somebody just typed.
func TestAChangeWaitingForARestartSaysSoAndSaysWhatItChanges(t *testing.T) {
	requested := time.Date(2026, 8, 8, 11, 0, 0, 0, time.UTC)
	payload, err := json.Marshal(map[string]any{"library.maintenance.interval_hours": 12})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueries{pendingConfig: app.GetPendingConfigVersionResult{
		Found:       true,
		ReloadClass: config.Restart,
		Changed:     []string{"library.maintenance.interval_hours"},
		Version: domain.ConfigVersion{
			ID: "cfg-1", Status: domain.ConfigPending,
			Payload: payload, RequestedAt: &requested,
		},
	}}
	svc := configService(fake)

	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))

	if !strings.Contains(text, "restart") {
		t.Errorf("a pending change does not say a restart applies it: %s", text)
	}
	// Naming the field is what makes the banner actionable rather than
	// ominous — "something is waiting" is not something anybody can check.
	if !strings.Contains(text, "library pass, every → 12") {
		t.Errorf("the banner does not say what is waiting: %s", text)
	}
}

// **The banner names what changed, not what the version contains.** A pending
// version is a whole configuration rather than a patch, so its payload carries
// every field including the ones nobody touched. Listing the payload's keys —
// which the first version of this panel did — told an operator who had changed
// the maintenance interval that a change was also waiting to set their log
// retention to the value it already had.
func TestTheBannerNamesOnlyWhatActuallyChanged(t *testing.T) {
	payload, err := json.Marshal(map[string]any{
		// The whole configuration, as a draft carries it.
		"telemetry.retention.logs_days":      21,
		"library.maintenance.items_per_run":  150,
		"library.maintenance.interval_hours": 8,
	})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueries{pendingConfig: app.GetPendingConfigVersionResult{
		Found:       true,
		ReloadClass: config.Restart,
		// Only one of the three differs from what is Active.
		Changed: []string{"library.maintenance.interval_hours"},
		Version: domain.ConfigVersion{
			ID: "cfg-1", Status: domain.ConfigPending, Payload: payload,
		},
	}}
	svc := configService(fake)

	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))

	if !strings.Contains(text, "library pass, every → 8") {
		t.Errorf("the banner does not name the field that changed: %s", text)
	}
	for _, unchanged := range []string{"keep logs for → 21", "library pass budget → 150"} {
		if strings.Contains(text, unchanged) {
			t.Errorf("the banner reported %q, which nobody changed: %s", unchanged, text)
		}
	}
}

// A field outside the curated set is named by its schema key rather than
// dropped. It cannot have been changed from this screen, so it came from
// somewhere else — and a banner that omitted it would say a change is waiting
// while declining to say what.
func TestAChangedFieldThisScreenDoesNotOfferIsStillNamed(t *testing.T) {
	payload, err := json.Marshal(map[string]any{"composition.modules": "tmdb,cinemeta"})
	if err != nil {
		t.Fatal(err)
	}
	fake := &fakeQueries{pendingConfig: app.GetPendingConfigVersionResult{
		Found:       true,
		ReloadClass: config.Generation,
		Changed:     []string{"composition.modules"},
		Version: domain.ConfigVersion{
			ID: "cfg-2", Status: domain.ConfigPending, Payload: payload,
		},
	}}
	svc := configService(fake)

	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))
	if !strings.Contains(text, "composition.modules → tmdb,cinemeta") {
		t.Errorf("a changed field this screen does not offer was not named: %s", text)
	}
	// And it must say the escalation it is waiting for does not exist, rather
	// than promising an upgrade nobody can trigger.
	if !strings.Contains(text, "not built yet") {
		t.Errorf("a generation-class pending change did not say its escalation is unbuilt: %s", text)
	}
}

// Nothing waiting is the ordinary state, and it must produce no banner rather
// than an empty one. A panel that always warned would train an operator to
// ignore the one time it mattered.
func TestNothingWaitingSaysNothing(t *testing.T) {
	svc := configService(&fakeQueries{})
	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))
	if strings.Contains(text, "is waiting") {
		t.Errorf("a panel with nothing pending drew a pending banner: %s", text)
	}
}

// Every field offered has a reader behind it. This is the test that keeps the
// list honest: `runtime.log_level` is in the schema, reads as the obvious thing
// to expose, and nothing in the Platform consults it — so a control for it
// would save a value, report that it applied, and change nothing.
func TestEveryOfferedFieldIsOneThePlatformActuallyReads(t *testing.T) {
	// The fields the Platform reads, gathered from the three readers' own
	// constants. A field added to this panel without a reader fails here.
	read := map[string]bool{
		"telemetry.retention.logs_days":       true,
		"telemetry.retention.traces_hours":    true,
		"telemetry.retention.metrics_days":    true,
		"telemetry.retention.audit_days":      true,
		"library.maintenance.interval_hours":  true,
		"library.maintenance.items_per_run":   true,
		"library.availability.interval_hours": true,
		"library.availability.items_per_run":  true,
	}
	schema := config.PlatformSchema()
	for _, field := range configurableFields {
		if !read[field.name] {
			t.Errorf("%q is offered on the configuration panel and nothing reads it", field.name)
		}
		if _, known := schema.ReloadClassOf(field.name); !known {
			t.Errorf("%q is offered and is not a registered configuration field", field.name)
		}
	}
}

// A caller who may not read configuration gets no nav row, rather than a row
// that opens on an error. The panel itself still authorises for itself — this
// is about not drawing an affordance nobody can use (platform#36).
func TestTheConfigurationRowIsDrawnOnlyForACallerWhoMayReadIt(t *testing.T) {
	fake := &fakeQueries{allow: map[string]bool{}}
	svc := &Service{content: fake, clock: time.Now}

	text := treeStrings(render(t, svc, "settings", nil))
	if strings.Contains(text, "Configuration") {
		t.Errorf("a caller without config.read was offered the Configuration section: %s", text)
	}
}

// The form is drawn only for a caller who can activate. Reading what the
// install is set to and changing it are separate grants, and a read-only
// administrator should see the numbers without a control that would be refused.
func TestTheFormIsDrawnOnlyForACallerWhoMayActivate(t *testing.T) {
	fake := &fakeQueries{allow: map[string]bool{
		string(app.ActionConfigRead): true,
	}}
	svc := &Service{content: fake, clock: time.Now}

	text := treeStrings(render(t, svc, "settings", map[string]any{"section": sectionConfiguration}))
	if !strings.Contains(text, "Keep logs for") {
		t.Errorf("a caller with config.read cannot see the configuration: %s", text)
	}
	if strings.Contains(text, "Apply") {
		t.Errorf("a caller without config.activate was offered the Apply control: %s", text)
	}
}
