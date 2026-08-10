// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"fmt"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The role-class table (platform#38).
//
// A core module fills a *role class*, and the class — not the module — declares
// how many implementations may be selected and what changing the selection
// costs. platform#38 is emphatic that this table lives in Platform code and **not**
// in module manifests: arity is a property of the class, not of any module that
// fills it, and a module has no business asserting "I am the only one of me" —
// a manifest that could would be a manifest that could lie. A module declares
// only which roles it fills; the Platform validates a selection against this.
//
// This is the single in-code copy of that table. It is data rather than
// scattered checks so there is one place to read what every class costs, and so
// the required-roles gate below is derived from it rather than from a hand-kept
// list of role constants — which is what the composition root used to carry.

// Arity is how many selected modules may fill a class.
type Arity int

const (
	// ArityExactlyOne is a class where the selection must name one and only one
	// implementation. Storage is the case: content does not move between engines
	// without a migration that does not exist.
	ArityExactlyOne Arity = iota
	// ArityAtLeastOne is a class where more than one implementation may be
	// selected and their results compose — several metadata providers union,
	// local and remote playback are complementary.
	ArityAtLeastOne
)

// Mutability is what changing a class's selection costs. It is carried for
// onboarding rather than enforced here: platform#38 requires that the storage
// choice be presented as one the user should get right and not imply a switch
// is coming, and a UI needs to read this to say so.
type Mutability int

const (
	// MutabilityDestructive is a change that loses data today — the storage
	// engine, where no migration exists (platform#38 declines to call it
	// permanently irreversible, but it is destructive now).
	MutabilityDestructive Mutability = iota
	// MutabilityGeneration is a change the Supervisor applies by activating a new
	// Generation of the same binary with a different selection (platform#38). It is
	// Generation-class in the reload-class vocabulary.
	MutabilityGeneration
)

// RoleClass is one row of the table.
type RoleClass struct {
	// Name is the class's stable identifier, for diagnostics and onboarding.
	Name string
	// Arity and Mutability are the class's properties, per platform#38.
	Arity      Arity
	Mutability Mutability
	// Required is whether a serving Mosaic must have this class filled. Metadata
	// and search are required as a class (platform#23): a Mosaic that cannot
	// identify or find content is inert, not degraded. Playback is not required
	// — a deployment with no consumer is discovery-only (platform#24), a degraded
	// state rather than a failed boot.
	Required bool
	// Roles are the capability roles this class comprises, each of which must be
	// filled for a required class. Empty means the class is not backed by the
	// capability registry — storage is a StorageAdapter, wired directly onto the
	// ContractSet, and its arity is enforced structurally by there being one
	// such field rather than by this table.
	Roles []v1.Role
}

// registryBacked reports whether this class is validated over the capability
// registry. Storage is the sole exception: it has no capability role and its
// exactly-one arity is enforced by the ContractSet holding a single
// StorageAdapter, which is a stronger guarantee than a runtime check — you
// cannot wire two.
func (c RoleClass) registryBacked() bool { return len(c.Roles) > 0 }

// RoleClasses is the table. It intentionally lists storage even though this
// package does not validate it, so the one authoritative copy of platform#38's
// table is complete rather than a subset that silently drops the row a reader
// most expects to find.
var RoleClasses = []RoleClass{
	{
		Name:       "storage",
		Arity:      ArityExactlyOne,
		Mutability: MutabilityDestructive,
		Required:   true,
		// No roles: enforced structurally at the ContractSet, not here.
	},
	{
		Name:       "metadata_search",
		Arity:      ArityAtLeastOne,
		Mutability: MutabilityGeneration,
		Required:   true,
		Roles:      []v1.Role{v1.RoleMetadata, v1.RoleSearch},
	},
	{
		Name:       "playback",
		Arity:      ArityAtLeastOne,
		Mutability: MutabilityGeneration,
		Required:   false,
		Roles:      []v1.Role{v1.RolePlayback},
	},
}

// RequireComposedRoleClasses fails when a required, registry-backed role class
// is not filled over the composed capability set — core and extension together
// (platform#38 re-expressing platform#23 over the selected set). It runs before the
// serve loop; a guarantee-clause core module means it passes on a fresh install
// with no configuration.
//
// It supersedes the composition root's hand-kept RequireRoles(RoleMetadata,
// RoleSearch): the required roles are now read from the table, so adding a
// required class is a table edit rather than a second place to remember. It
// delegates to RequireRoles for the actual scan so there is one implementation
// of "is this role filled", and wraps the result with the class name so a boot
// failure says which class is short rather than only which role.
func (r *CapabilityRegistry) RequireComposedRoleClasses() error {
	for _, class := range RoleClasses {
		if !class.Required || !class.registryBacked() {
			continue
		}
		if err := r.RequireRoles(class.Roles...); err != nil {
			return fmt.Errorf("required %q role class not filled: %w", class.Name, err)
		}
	}
	return nil
}
