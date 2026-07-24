// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protojson"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/domain"
)

// The expert-mode surface (ADR 0058). The visibility rule is the part worth
// testing hardest: a user without telemetry.read must not even see the toggle.

func TestExpertModeAffordanceIsHiddenWithoutTheGrant(t *testing.T) {
	fake := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: false}
	svc := &Service{content: fake}

	node := render(t, svc, "settings", nil)
	if rendered := nodeText(node); strings.Contains(rendered, "Expert mode") {
		t.Fatalf("a caller without telemetry.read was offered the expert-mode affordance: %s", rendered)
	}
}

func TestExpertModeAffordanceAppearsWithTheGrant(t *testing.T) {
	fake := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: true}
	svc := &Service{content: fake}

	node := render(t, svc, "settings", nil)
	rendered := nodeText(node)
	for _, want := range []string{"Expert mode", "Turn on expert mode"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q in the settings screen: %s", want, rendered)
		}
	}
	// Off by default, so the links it governs are not offered yet.
	if strings.Contains(rendered, screenLogs) {
		t.Fatalf("diagnostics links should wait until expert mode is on: %s", rendered)
	}
}

// TestExpertModeLinksAppearOnlyWhenItIsOn separates the two questions the
// section answers: the permission decided the toggle is visible, the preference
// decides whether the surface it governs is offered.
func TestExpertModeLinksAppearOnlyWhenItIsOn(t *testing.T) {
	fake := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: true, expertModeOn: true}
	svc := &Service{content: fake}

	rendered := nodeText(render(t, svc, "settings", nil))
	for _, want := range []string{"Turn off expert mode", "Logs", "Traces"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("expected %q once expert mode is on: %s", want, rendered)
		}
	}
}

// TestExpertModeToggleIsHiddenWithoutTheGrantEvenIfEnabled — the preference
// must never be able to reveal the affordance on its own. Someone whose grant
// was revoked keeps the stored preference, and must still see nothing.
func TestExpertModeToggleIsHiddenWithoutTheGrantEvenIfEnabled(t *testing.T) {
	fake := &fakeQueries{settingsUI: minimalSettingsUI(), canReadTelemetry: false, expertModeOn: true}
	svc := &Service{content: fake}

	if rendered := nodeText(render(t, svc, "settings", nil)); strings.Contains(rendered, "Expert mode") {
		t.Fatalf("a stored preference must not reveal the affordance without the grant: %s", rendered)
	}
}

// TestLogsScreenPassesItsFilterThrough — the filter controls are navigations
// carrying params, so a param that never reaches the query is a control that
// silently does nothing.
func TestLogsScreenPassesItsFilterThrough(t *testing.T) {
	fake := &fakeQueries{logs: []domain.TelemetryLogRecord{{
		Time: time.Now(), Level: "error", Component: "session",
		Message: "stream failed", Trace: "abc123def456", Fields: []byte(`{"elapsed":"3ms"}`),
	}}}
	svc := &Service{content: fake}

	render(t, svc, "logs", map[string]any{
		"level": "warn", "component": "session", "text": "failed",
	})
	got := fake.gotLogFilter
	if got.MinLevel != "warn" || got.Component != "session" || got.Contains != "failed" {
		t.Fatalf("filter did not reach the query: %+v", got)
	}
}

// TestLogRowOffersItsTrace is the move that makes a log line useful: "what else
// happened because of this" should be a tap, not a copied string.
func TestLogRowOffersItsTrace(t *testing.T) {
	fake := &fakeQueries{logs: []domain.TelemetryLogRecord{{
		Time: time.Now(), Level: "error", Component: "session",
		Message: "stream failed", Trace: "abc123def456",
	}}}
	svc := &Service{content: fake}

	node := render(t, svc, "logs", nil)
	if rendered := nodeText(node); !strings.Contains(rendered, "abc123de") {
		t.Fatalf("expected a navigation to the record's trace: %s", rendered)
	}
}

// TestTraceScreenNestsSpansByParent covers the waterfall's shape. The entry
// span's parent is the *client's* span and was never stored here, so it must be
// treated as a root rather than dropped.
func TestTraceScreenNestsSpansByParent(t *testing.T) {
	now := time.Now()
	fake := &fakeQueries{spans: []domain.TelemetrySpanRecord{
		{Span: "root", Parent: "client-span-not-stored", Name: "Navigate", Time: now, Duration: 500 * time.Millisecond, Status: "ok"},
		{Span: "mod", Parent: "root", Name: "module.search", Time: now.Add(time.Millisecond), Duration: 460 * time.Millisecond, Status: "ok", Module: "stremio"},
		{Span: "http", Parent: "mod", Name: "http GET example", Time: now.Add(2 * time.Millisecond), Duration: 320 * time.Millisecond, Status: "ok"},
	}}
	svc := &Service{content: fake}

	node := render(t, svc, "trace", map[string]any{"trace": "t-1"})
	rendered := nodeText(node)

	for _, want := range []string{"Navigate", "module.search", "http GET example"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("span %q missing from the waterfall: %s", want, rendered)
		}
	}
	// Indentation carries the tree, and it lives inside the label rather than
	// across lines — so assert the exact depths rather than counting columns.
	// A flat list would render every name at depth zero.
	if !strings.Contains(rendered, `"    module.search"`) {
		t.Fatalf("module.search should sit one level in: %s", rendered)
	}
	if !strings.Contains(rendered, `"        http GET example"`) {
		t.Fatalf("the HTTP span should sit two levels in, under the module: %s", rendered)
	}
	// The share of the whole is the point of a waterfall — a duration alone
	// does not say which part *was* the time.
	if !strings.Contains(rendered, "%") {
		t.Fatalf("expected each span's share of the total: %s", rendered)
	}
}

// TestTraceScreenKeepsOrphanedSpans — retention can drop a parent partition
// while its children survive, and a waterfall missing rows is worse than one
// with a few unplaced.
func TestTraceScreenKeepsOrphanedSpans(t *testing.T) {
	now := time.Now()
	fake := &fakeQueries{spans: []domain.TelemetrySpanRecord{
		{Span: "a", Parent: "", Name: "root-span", Time: now, Duration: time.Second, Status: "ok"},
		{Span: "orphan", Parent: "long-since-dropped", Name: "orphan-span", Time: now, Duration: time.Millisecond, Status: "ok"},
	}}
	svc := &Service{content: fake}

	node := render(t, svc, "trace", map[string]any{"trace": "t-1"})
	if rendered := nodeText(node); !strings.Contains(rendered, "orphan-span") {
		t.Fatalf("an orphaned span was dropped from the waterfall: %s", rendered)
	}
}

// nodeText renders a screen to its wire JSON, which is what the assertions
// above match against. Testing the payload rather than a prose rendering is
// deliberate: the payload is the contract a client renders (ADR 0029), so a
// label that vanished from it is a label no client can show.
func nodeText(n sdui.Node) string {
	raw, err := protojson.Marshal(n)
	if err != nil {
		return ""
	}
	return string(raw)
}

// minimalSettingsUI is the smallest valid module-contributed settings tree, so
// the settings screen has something to host while these tests exercise what is
// appended beside it.
func minimalSettingsUI() []byte {
	raw, err := protojson.Marshal(ui.Component("ModuleSettings", ui.Title("Stremio")).Build())
	if err != nil {
		panic(err)
	}
	return raw
}
