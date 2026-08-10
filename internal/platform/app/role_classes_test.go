// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app_test

import (
	"strings"
	"testing"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"

	"github.com/mosaic-media/platform/internal/platform/app"
)

// These reuse everythingCapability from capability_registry_test.go: it
// implements every provider interface and declares whatever its manifest says,
// so a test composes known coverage by choosing the manifest's Provides. That
// it implements everything is exactly why the registry gates on the manifest —
// see fills() — and it is what makes "declares metadata+search" a real test of
// the required-class check.

// The role-class table is the single in-code copy of platform#38's table, so a
// test guards its shape against a careless edit: the three classes, their
// arities, and which is required.
func TestRoleClassTableMatchesADR0063(t *testing.T) {
	byName := map[string]app.RoleClass{}
	for _, c := range app.RoleClasses {
		byName[c.Name] = c
	}

	storage, ok := byName["storage"]
	if !ok {
		t.Fatal("the table dropped the storage class")
	}
	if storage.Arity != app.ArityExactlyOne {
		t.Error("storage must be exactly-one: content does not move between engines without a migration")
	}
	if storage.Mutability != app.MutabilityDestructive {
		t.Error("storage is destructive to change today")
	}
	if len(storage.Roles) != 0 {
		t.Error("storage has no capability role; its arity is enforced at the ContractSet")
	}

	ms, ok := byName["metadata_search"]
	if !ok {
		t.Fatal("the table dropped the metadata_search class")
	}
	if !ms.Required {
		t.Error("metadata_search is required as a class (platform#23)")
	}
	if ms.Arity != app.ArityAtLeastOne {
		t.Error("metadata_search composes: several providers union")
	}

	pb, ok := byName["playback"]
	if !ok {
		t.Fatal("the table dropped the playback class")
	}
	if pb.Required {
		t.Error("playback is not required: a deployment with no consumer is discovery-only (platform#24), not a failed boot")
	}
}

// The boot check passes when the required classes are filled, and the roles it
// checks come from the table rather than a hand-kept list.
func TestRequireComposedRoleClassesPassesWhenFilled(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "meta",
		Provides: []v1.Role{v1.RoleMetadata, v1.RoleSearch},
	}})

	if err := reg.RequireComposedRoleClasses(); err != nil {
		t.Fatalf("a registry filling metadata and search should pass: %v", err)
	}
}

// It fails, naming the class, when a required class is short — the failure a
// fresh install must never reach silently (platform#23).
func TestRequireComposedRoleClassesFailsWhenAClassIsShort(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	// Search only: the metadata_search class needs both roles.
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "search-only",
		Provides: []v1.Role{v1.RoleSearch},
	}})

	err := reg.RequireComposedRoleClasses()
	if err == nil {
		t.Fatal("a registry missing RoleMetadata passed the required-class check")
	}
	if !strings.Contains(err.Error(), "metadata_search") {
		t.Errorf("the error should name the short class, got: %v", err)
	}
}

// Playback being unfilled is not a boot failure, because it is not required: a
// deployment with no consumer is discovery-only (platform#24), a degraded state
// rather than an inert one.
func TestUnfilledPlaybackIsNotFatal(t *testing.T) {
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{
		ID:       "meta",
		Provides: []v1.Role{v1.RoleMetadata, v1.RoleSearch},
	}})
	// No playback provider registered.

	if err := reg.RequireComposedRoleClasses(); err != nil {
		t.Fatalf("a missing playback consumer must not fail boot (platform#24): %v", err)
	}
}

// The required roles the check enforces are exactly those the table marks
// required — so adding a required class is a table edit, and this asserts the
// two do not drift.
func TestRequiredRolesComeFromTheTable(t *testing.T) {
	want := map[v1.Role]bool{}
	for _, c := range app.RoleClasses {
		if c.Required {
			for _, role := range c.Roles {
				want[role] = true
			}
		}
	}

	// A registry filling every required role must pass; dropping any one must
	// fail. That is the property "the check is the table" reduces to.
	all := make([]v1.Role, 0, len(want))
	for role := range want {
		all = append(all, role)
	}
	reg := app.NewCapabilityRegistry()
	reg.Register(&everythingCapability{manifest: v1.Manifest{ID: "all", Provides: all}})
	if err := reg.RequireComposedRoleClasses(); err != nil {
		t.Fatalf("a registry filling every required role failed: %v", err)
	}

	for dropped := range want {
		remaining := make([]v1.Role, 0, len(want)-1)
		for role := range want {
			if role != dropped {
				remaining = append(remaining, role)
			}
		}
		short := app.NewCapabilityRegistry()
		short.Register(&everythingCapability{manifest: v1.Manifest{ID: "short", Provides: remaining}})
		if err := short.RequireComposedRoleClasses(); err == nil {
			t.Errorf("dropping required role %q still passed the check", dropped)
		}
	}
}
