package config

// ReloadClass describes how a change to a configuration field takes effect
// (MEG-015 §08 — Reload Classes). Every field the Platform schema exposes
// must declare one, so callers can ask "does this change require a
// restart" structurally rather than relying on documentation.
type ReloadClass string

const (
	// Hot changes apply without a restart.
	Hot ReloadClass = "hot"
	// Restart changes require a process restart.
	Restart ReloadClass = "restart"
	// Generation changes require the Supervisor to activate a new
	// Generation (MIP-006 — Generation Composition Protocol).
	Generation ReloadClass = "generation"
	// Recovery changes apply only through the recovery flow.
	Recovery ReloadClass = "recovery"
)

// reloadClassRank orders reload classes by increasing operational
// involvement required to apply a change, matching the order MEG-015 §08's
// table lists them in. It is used to pick the most restrictive class among
// a set of changed fields.
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
