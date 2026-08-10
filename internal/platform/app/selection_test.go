// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"strings"
	"testing"

	"github.com/mosaic-media/platform/internal/platform/app"
)

// The default selects everything, because a fresh install must work with
// nothing configured (module-cinemeta#1): the zero-config metadata floor is a core
// module, and a default that dropped it would boot an inert Mosaic.
func TestSelectAllSelectsEverything(t *testing.T) {
	s := app.SelectAll()
	for _, id := range []string{"cinemeta", "tmdb", "anything-at-all"} {
		if !s.Selected(id) {
			t.Errorf("SelectAll did not select %q", id)
		}
	}
}

func TestSelectOnlySelectsExactlyThose(t *testing.T) {
	s := app.SelectOnly("cinemeta", "remote-playback")
	if !s.Selected("cinemeta") || !s.Selected("remote-playback") {
		t.Error("a named module was not selected")
	}
	if s.Selected("tmdb") {
		t.Error("an unnamed module was selected")
	}
}

// An explicit empty selection is nothing, not everything — the distinction that
// keeps "unset" (all) separate from "set to empty" (none).
func TestEmptySelectionSelectsNothing(t *testing.T) {
	s := app.SelectOnly()
	if s.Selected("cinemeta") {
		t.Error("an empty explicit selection selected a module")
	}
}

func TestParseSelectionTrimsAndIgnoresBlanks(t *testing.T) {
	s := app.ParseSelection("cinemeta, tmdb ,, remote-playback")
	for _, id := range []string{"cinemeta", "tmdb", "remote-playback"} {
		if !s.Selected(id) {
			t.Errorf("ParseSelection dropped %q", id)
		}
	}
	if s.Selected("aiostreams") {
		t.Error("ParseSelection selected something it was not given")
	}
}

// A selection naming a module the binary does not carry is a config error, not
// a silent drop: the typo would otherwise disable the module it misspelled and
// say nothing.
func TestValidateRejectsUnknownModule(t *testing.T) {
	known := []string{"cinemeta", "tmdb", "remote-playback"}
	err := app.SelectOnly("cinemeta", "tmbd").Validate(known) // "tmbd" is a typo
	if err == nil {
		t.Fatal("a selection naming an unknown module was accepted")
	}
	if !strings.Contains(err.Error(), "tmbd") {
		t.Errorf("the error should name the unknown id, got: %v", err)
	}
}

func TestValidateAcceptsKnownModules(t *testing.T) {
	known := []string{"cinemeta", "tmdb", "remote-playback"}
	if err := app.SelectOnly("cinemeta", "remote-playback").Validate(known); err != nil {
		t.Fatalf("a selection of known modules was rejected: %v", err)
	}
}

// SelectAll validates against any set, since it names no ids to be wrong about.
func TestSelectAllValidatesAgainstAnything(t *testing.T) {
	if err := app.SelectAll().Validate(nil); err != nil {
		t.Fatalf("SelectAll should validate against any known set: %v", err)
	}
}
