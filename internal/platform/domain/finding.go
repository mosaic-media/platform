// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// Operational findings (platform#74): what is wrong with this install, now.
//
// An Issue is a fact about the present, which is what separates it from
// everything else the Platform records. Telemetry answers "what happened at
// 04:12", an event is a fact about a moment, and health answers "should
// traffic come here"; none of them has current state, closure or an addressee.
//
// The failure and the remedy are one record. An Issue that offers no
// Suggestion and demands no decision is a log line with a table behind it and
// belongs in telemetry instead — that is the standing rule against filing
// noise here.

// IssueType is what is wrong, as a closed vocabulary.
//
// Closed by [platform#11](https://github.com/mosaic-media/platform/blob/main/docs/adr/0011-open-and-closed-vocabularies.md)'s
// test — Platform code branches on it to decide which Suggestions to offer, and
// a client branches on it to choose words. An open set would let a subsystem
// state a problem no client can render and no code can act on, which fails open
// in the one place that exists to prevent exactly that.
type IssueType string

const (
	// IssueExtensionUnavailable is an installed extension module that could not
	// be brought up. The capability it filled is simply absent, which is a
	// degradation nothing else reports: extensions fill no required role, so
	// nothing fails and nothing is said.
	IssueExtensionUnavailable IssueType = "extension_unavailable"
	// IssueChildUnrecoverable is a supervised process the Supervisor has
	// stopped expecting to come up. It is still retrying; what has changed is
	// that the situation is now stated rather than repeated.
	IssueChildUnrecoverable IssueType = "child_unrecoverable"
	// IssueGenerationRolledBack is an upgrade that was applied, failed its
	// health check and was reverted. The install is working, on the version it
	// had before, so without a record the user's report is "it didn't update".
	IssueGenerationRolledBack IssueType = "generation_rolled_back"
	// IssueProvisionFailed is a first boot that could not fetch or verify a
	// Generation. Nothing is running, so nothing else can report it.
	IssueProvisionFailed IssueType = "provision_failed"
	// IssueUpgradeAvailable is a newer release the signed catalogue offers and
	// this install has not taken (platform#77).
	//
	// It is the one entry here that is not a fault. It is a finding anyway
	// because the register is already the surface for "something needs your
	// attention" and already folds repeats into one row with a count; the
	// alternative was a notification channel built for one feature. Its
	// Suggestion acts rather than acknowledging — see SuggestionApplyUpgrade.
	IssueUpgradeAvailable IssueType = "upgrade_available"
	// IssueUpgradeFailed is a requested upgrade that did not install. Distinct
	// from IssueGenerationRolledBack, which is one that installed, came up
	// badly and was reverted: this one never got that far, so the remedy and
	// the reason are different.
	IssueUpgradeFailed IssueType = "upgrade_failed"
)

// KnownIssueTypes is the closed set, in the order a screen lists them. It is
// enumerated rather than derived: a validator that accepted whatever was
// written would enforce nothing.
var KnownIssueTypes = []IssueType{
	IssueProvisionFailed,
	IssueUpgradeFailed,
	IssueUpgradeAvailable,
	IssueGenerationRolledBack,
	IssueChildUnrecoverable,
	IssueExtensionUnavailable,
}

// Valid reports whether the type is one this build knows.
func (t IssueType) Valid() bool {
	for _, known := range KnownIssueTypes {
		if t == known {
			return true
		}
	}
	return false
}

// SuggestionType is a named action that might resolve an Issue.
//
// It carries no prose: the client renders a type into whatever words and
// affordance suit it, so it can be translated. A suggestion whose meaning
// lives in a string the server wrote is one only that client can present.
type SuggestionType string

const (
	// SuggestionReinstallExtension re-fetches and re-verifies a module whose
	// cached binary is gone or no longer matches what was signed for.
	SuggestionReinstallExtension SuggestionType = "reinstall_extension"
	// SuggestionUninstallExtension removes a module that will not come up, so
	// the install stops carrying a capability it does not have.
	SuggestionUninstallExtension SuggestionType = "uninstall_extension"
	// SuggestionApplyUpgrade asks the Supervisor to install the version this
	// Issue is about (platform#77).
	//
	// The Platform cannot perform it. Pressing this records a request naming
	// the version, the handoff reports it, and the Supervisor — the only
	// process that can stop and restart the Platform — carries it out. The
	// request settles when the Platform is running the version it asked for.
	SuggestionApplyUpgrade SuggestionType = "apply_upgrade"
	// SuggestionDismiss closes an Issue without changing anything. It is a real
	// answer: "I know, and I am choosing to live with it" is a decision, and an
	// Issue that cannot be dismissed becomes furniture nobody reads.
	SuggestionDismiss SuggestionType = "dismiss"
)

// IssueContext is what an Issue is about, as a closed vocabulary. It pairs with
// Reference: the context says which kind of thing, the reference names the one.
type IssueContext string

const (
	// ContextExtension is one extension module, referenced by module id.
	ContextExtension IssueContext = "extension"
	// ContextChild is one supervised process, referenced by child name.
	ContextChild IssueContext = "child"
	// ContextGeneration is one release Generation, referenced by version.
	ContextGeneration IssueContext = "generation"
	// ContextHost is the machine itself, with no reference.
	ContextHost IssueContext = "host"
)

// IssueSource is which process detected the finding.
//
// It matters on the screen: "the Supervisor could not start the Platform" and
// "the Platform could not start a module" are different situations with
// different remedies, and a register that flattened them would make the first
// unreadable.
type IssueSource string

const (
	SourcePlatform   IssueSource = "platform"
	SourceSupervisor IssueSource = "supervisor"
)

// Issue is a durable, typed statement that something is wrong.
//
// It persists until something clears it, and it is created at the point of
// detection by the code that detected it — never by a scanner sweeping for
// problems afterwards, which can only find what it was written to look for and
// finds it long after the context is gone.
type Issue struct {
	ID string
	// Type, Context and Reference together are the identity of a situation.
	// Raising the same three twice is the same Issue happening again, not a
	// second one: a module that fails to start on every boot must be one row
	// that says "since Tuesday", not one row per boot.
	Type      IssueType
	Context   IssueContext
	Reference string
	// Source is the process that detected it.
	Source IssueSource
	// Detail is one sentence of what actually happened — the error, the
	// version, the path. It is context for a person, never the meaning:
	// nothing branches on it, and a client that cannot render it still knows
	// what the Issue is from its type.
	Detail string
	// Suggestions are the actions offered, derived from the type rather than
	// stored: what a build can do about a situation is a property of that
	// build, so a row written by an older one must not pin an offer this one
	// no longer honours.
	Suggestions []SuggestionType
	// FirstSeen is when this situation started, preserved across re-raises.
	// LastSeen is the most recent detection, which is what tells a person
	// whether it is still happening.
	FirstSeen time.Time
	LastSeen  time.Time
	// Occurrences counts detections. A module that failed once and a module
	// that has failed on every boot for a fortnight are different problems and
	// the row otherwise reads identically.
	Occurrences int
}

// SuggestionsFor returns what this build can offer for an issue type. It is
// derived rather than stored so that a row written by an older build cannot
// pin an offer this one no longer honours: a withdrawn suggestion disappears
// from an existing Issue instead of failing when pressed.
//
// Dismiss is on every type, deliberately: an Issue nobody can close becomes
// furniture.
func SuggestionsFor(t IssueType) []SuggestionType {
	switch t {
	case IssueExtensionUnavailable:
		return []SuggestionType{SuggestionReinstallExtension, SuggestionUninstallExtension, SuggestionDismiss}
	case IssueUpgradeAvailable:
		// Dismiss stands beside it because declining a version is a real
		// answer. The offer returns on the next check, which is correct: the
		// version is still there and still not installed.
		return []SuggestionType{SuggestionApplyUpgrade, SuggestionDismiss}
	case IssueChildUnrecoverable, IssueGenerationRolledBack, IssueProvisionFailed, IssueUpgradeFailed:
		// Nothing this build can do about these from a screen: restarting a
		// child and re-running an upgrade are the Supervisor's, and it has no
		// action lane (supervisor#7 gave it a read surface and no more). A
		// control that did nothing would be worse than none.
		return []SuggestionType{SuggestionDismiss}
	default:
		return []SuggestionType{SuggestionDismiss}
	}
}

// UpgradeRequest is somebody asking for a version to be installed
// (platform#77).
//
// It names a version and never "latest". The Platform does not hold the
// release catalogue, so it asks for the version it was offered, and the
// Supervisor resolves that name against the signed catalogue rather than
// trusting the request — which is what stops a request pointing an install at
// bytes nobody signed for.
type UpgradeRequest struct {
	ID      string
	Version string
	// RequestedBy is the user id that pressed the control, for the record. An
	// upgrade is the most consequential thing a non-destructive control does,
	// and "who asked for this" is the first question afterwards.
	RequestedBy string
	RequestedAt time.Time
}
