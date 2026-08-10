// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"fmt"
	"sort"
	"strings"
)

// Selection is which core modules a composition wires in (platform#38).
//
// A core module the composition does not select is never constructed and never
// registered: no registry holds it, it cannot be resolved by role, and it
// cannot appear in a capability-gated affordance. It is code in the binary that
// never boots.
//
// One honesty about the Go model, since platform#38 says "no init runs for it": a
// compiled-in module's *package* is imported, so its package-level init runs
// regardless — that is unavoidable for anything statically linked. What
// selection skips is the module's construction (its New) and its registration,
// which is where every resource and every effect lives. "Code in the binary
// that never boots" is exact; "no init runs" is exact for the module's own
// initialization and loose for Go's package init, and this is the distinction.
//
// Selection is Generation-class configuration in the reload-class vocabulary:
// an admin changes it, and the Supervisor activates a new Generation of the
// same binary with a different selection rather than building a candidate (ADR
// 0063). The Generation-activation half is Supervisor-shaped and unbuilt; what
// is built here is the selection itself and its validation.
type Selection struct {
	// all is true when no explicit selection was made — every core module the
	// binary carries is wired in. It is the default because a fresh install must
	// work with nothing configured (module-cinemeta#1): the zero-configuration metadata
	// floor is a core module, and a default that dropped it would boot an inert
	// Mosaic.
	all bool
	// ids is the explicit set, used only when all is false.
	ids map[string]bool
}

// SelectAll wires in every core module. It is the default, and what an
// unconfigured deployment gets.
func SelectAll() Selection { return Selection{all: true} }

// SelectOnly wires in exactly the named modules and no others. An empty call
// selects nothing, which is a valid (if unusual) request the caller is trusted
// to have meant — RequireComposedRoleClasses is what refuses a selection that
// leaves a required class empty, so this type does not also police it.
func SelectOnly(ids ...string) Selection {
	set := make(map[string]bool, len(ids))
	for _, id := range ids {
		set[id] = true
	}
	return Selection{ids: set}
}

// ParseSelection reads a selection from a comma-separated spec. Whitespace
// around each id is trimmed and empty entries are ignored, so "a, b ,,c" is
// three ids. A spec of exactly "" is an explicit empty selection, not "all":
// the caller distinguishes an unset source from a set-but-empty one and calls
// SelectAll for the former.
func ParseSelection(spec string) Selection {
	var ids []string
	for _, part := range strings.Split(spec, ",") {
		if id := strings.TrimSpace(part); id != "" {
			ids = append(ids, id)
		}
	}
	return SelectOnly(ids...)
}

// Selected reports whether id is wired in.
func (s Selection) Selected(id string) bool {
	if s.all {
		return true
	}
	return s.ids[id]
}

// Validate rejects a selection that names a module the binary does not carry.
// A typo in a selection would otherwise silently disable a module the admin
// meant to keep — the selection would name a ghost, the real module would be
// dropped for not being named, and nothing would say so. Known is the set of
// core module ids the composition offers.
func (s Selection) Validate(known []string) error {
	if s.all {
		return nil
	}
	knownSet := make(map[string]bool, len(known))
	for _, id := range known {
		knownSet[id] = true
	}
	var unknown []string
	for id := range s.ids {
		if !knownSet[id] {
			unknown = append(unknown, id)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return fmt.Errorf(
			"module selection names %s, which the binary does not carry; known core modules are %s",
			strings.Join(unknown, ", "), strings.Join(sortedCopy(known), ", "))
	}
	return nil
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	sort.Strings(out)
	return out
}
