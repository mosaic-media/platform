// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package config

// ReloadClass describes how a change to a configuration field takes effect.
// Every field the Platform schema exposes must declare one, so callers can
// ask "does this change require a restart" structurally rather than relying
// on documentation.
type ReloadClass string

const (
	// Hot changes apply without a restart.
	Hot ReloadClass = "hot"
	// Restart changes require a process restart.
	Restart ReloadClass = "restart"
	// Generation changes require the Supervisor to activate a new
	// Generation.
	Generation ReloadClass = "generation"
	// Recovery changes apply only through the recovery flow.
	Recovery ReloadClass = "recovery"
)

// reloadClassRank orders reload classes by increasing operational
// involvement required to apply a change. It is used to pick the most
// restrictive class among a set of changed fields.
var reloadClassRank = map[ReloadClass]int{
	Hot:        0,
	Restart:    1,
	Generation: 2,
	Recovery:   3,
}

// valid reports whether c is one of the four declared reload classes.
func (c ReloadClass) valid() bool {
	_, ok := reloadClassRank[c]
	return ok
}

// moreRestrictive returns whichever of a and b requires more operational
// involvement to apply.
func moreRestrictive(a, b ReloadClass) ReloadClass {
	if reloadClassRank[b] > reloadClassRank[a] {
		return b
	}
	return a
}

// RequiredClassBetween reports the most restrictive reload class among the
// fields that differ between two configuration payloads, using the Platform
// schema.
//
// It exists so a caller outside this package — the Supervisor handoff surface,
// which has to say what escalation is owed — can ask the question without
// holding a Manager or reimplementing the diff. A malformed payload answers
// Recovery: the most restrictive class is the safe direction to fail, because
// it stops short of claiming a change can be applied more cheaply than it can.
func RequiredClassBetween(current, candidate []byte) ReloadClass {
	changed, err := ChangedFields(current, candidate)
	if err != nil {
		return Recovery
	}
	class, _ := PlatformSchema().RequiredReloadClass(changed)
	return class
}
