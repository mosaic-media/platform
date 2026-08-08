// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package session

import (
	"errors"
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/config"
	"github.com/mosaic-media/platform/internal/platform/contracts"
)

// **The load-bearing test in this file.** Every reader on the far side takes a
// JSON *number* — `durationField` and `countField` both type-assert float64 —
// and validation checks only that a field is registered, never what type it
// holds. So a retention submitted as the string "30" validates, activates,
// reports "Applied." and is then silently ignored by the reader it was meant to
// change. A form's values arrive as strings, so this conversion is the only
// thing standing between the two.
func TestNumbersArriveAsTextAndAreStoredAsNumbers(t *testing.T) {
	fields, err := configFieldsFromInput([]byte(`{"telemetry.retention.logs_days":"30"}`))
	if err != nil {
		t.Fatalf("configFieldsFromInput: %v", err)
	}
	got, ok := fields["telemetry.retention.logs_days"]
	if !ok {
		t.Fatal("the field was dropped")
	}
	if _, isString := got.(string); isString {
		t.Fatalf("stored as text (%q) — the reader takes a number and would ignore it", got)
	}
	if got != 30 {
		t.Errorf("stored %v, want 30", got)
	}
}

// A value that is not a number is a rejection on the box that carries it, not a
// generic refusal — a form with eight inputs on it must say which one is wrong
// (ADR 0089).
func TestANonNumberIsRejectedOnItsOwnField(t *testing.T) {
	_, err := configFieldsFromInput([]byte(`{"telemetry.retention.logs_days":"a fortnight"}`))
	if err == nil {
		t.Fatal("a non-numeric value was accepted")
	}
	var rejection *contracts.Error
	if !errors.As(err, &rejection) || len(rejection.Fields) == 0 {
		t.Fatalf("the refusal does not name a field: %v", err)
	}
	if len(rejection.Fields) != 1 || rejection.Fields[0].Field != "telemetry.retention.logs_days" {
		t.Errorf("rejected %+v", rejection.Fields)
	}
}

// An empty box means "leave this alone". A form carries every field in its
// scope on every submit, so writing the empty ones would have somebody changing
// one number silently re-state — or erase — the other seven.
func TestAnEmptyBoxLeavesTheSettingAlone(t *testing.T) {
	fields, err := configFieldsFromInput([]byte(
		`{"telemetry.retention.logs_days":"30","library.maintenance.items_per_run":"  "}`))
	if err != nil {
		t.Fatalf("configFieldsFromInput: %v", err)
	}
	if _, present := fields["library.maintenance.items_per_run"]; present {
		t.Error("an empty box was submitted as a value")
	}
	if len(fields) != 1 {
		t.Errorf("fields = %v, want only the one that was filled", fields)
	}
}

// A submit carries the form's own error binding alongside the fields, so an
// unknown key is a rendering detail rather than a mistake. Refusing the call
// for it would turn "the form has an error variable" into an error nobody can
// act on.
func TestAKeyTheSchemaDoesNotKnowIsDroppedRatherThanRefused(t *testing.T) {
	fields, err := configFieldsFromInput([]byte(
		`{"formError":"","telemetry.retention.logs_days":"30"}`))
	if err != nil {
		t.Fatalf("an unknown key was refused: %v", err)
	}
	if _, present := fields["formError"]; present {
		t.Error("the form's own error variable was submitted as configuration")
	}
	if len(fields) != 1 {
		t.Errorf("fields = %v", fields)
	}
}

// What the operator is told, per class. The Hot case is the only one where the
// change has actually happened; every other answer has to name what will apply
// it, because "Saved." on its own reads as "done".
func TestTheSentenceSaysWhetherAnythingActuallyHappened(t *testing.T) {
	if got := appliedSentence(app.ActivateConfigVersionResult{Activated: true}); got != "Applied." {
		t.Errorf("a hot change said %q", got)
	}
	restart := appliedSentence(app.ActivateConfigVersionResult{ReloadClass: config.Restart})
	if restart == "Applied." {
		t.Fatal("a change that has not been applied reported that it had")
	}
	if !strings.Contains(restart, "restart") {
		t.Errorf("a restart-class change said %q without naming a restart", restart)
	}
	// The Generation case has to say the escalation does not exist yet.
	// Reporting "takes effect on the next upgrade" for a mechanism nobody can
	// trigger is a promise the Platform cannot keep.
	generation := appliedSentence(app.ActivateConfigVersionResult{ReloadClass: config.Generation})
	if !strings.Contains(generation, "not built") {
		t.Errorf("a generation-class change said %q — the escalation it names does not exist", generation)
	}
}
