// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import "time"

// Operational findings (ADR 0119): what is wrong with this install, now.
//
// **An Issue is a fact about the present, and that is what separates it from
// everything else Mosaic already records.** Telemetry answers "what happened at
// 04:12" and is genuinely good at it; an event is a fact about a moment; health
// answers "should traffic come here". None of them can answer "what is wrong
// with my server" — a log has no current state, no closure and no addressee, so
// a failure a user cannot act on is a failure they report to somebody else,
// three days after the evidence rotated away.
//
// **The failure and the remedy are one record.** An Issue that offers no
// Suggestion and demands no decision is a log line with a table behind it, and
// belongs in telemetry instead. That is the standing rule against filing noise
// here, and it is the one this register is most likely to break.

// IssueType is what is wrong, as a closed vocabulary.
//
// Closed by [ADR 0015](https://github.com/mosaic-media/architecture/blob/main/docs/adr/0015-open-and-closed-vocabularies.md)'s
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
	// health check and was reverted. **This is the case ADR 0119 was written
	// for**: the install is working, it is working on the version it had
	// before, and without a record the user's report is "it didn't update".
	IssueGenerationRolledBack IssueType = "generation_rolled_back"
	// IssueProvisionFailed is a first boot that could not fetch or verify a
	// Generation. Nothing is running, so nothing else can report it.
	IssueProvisionFailed IssueType = "provision_failed"
)

// KnownIssueTypes is the closed set, in the order a screen lists them.
//
// Enumerated rather than derived, because the set being *closed* is the
// property worth having and a validator that accepted whatever was written
// would enforce nothing.
var KnownIssueTypes = []IssueType{
	IssueProvisionFailed,
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
// **It carries no prose.** The client renders a type into whatever words and
// affordance suit it, which is what keeps this out of the SDUI's way and lets a
// future client translate it. A suggestion whose meaning lives in a string the
// server wrote is a suggestion only one client can present.
type SuggestionType string

const (
	// SuggestionReinstallExtension re-fetches and re-verifies a module whose
	// cached binary is gone or no longer matches what was signed for.
	SuggestionReinstallExtension SuggestionType = "reinstall_extension"
	// SuggestionUninstallExtension removes a module that will not come up, so
	// the install stops carrying a capability it does not have.
	SuggestionUninstallExtension SuggestionType = "uninstall_extension"
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
	// version, the path. **It is context for a person, never the meaning**:
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

// SuggestionsFor returns what this build can offer for an issue type.
//
// **Derived, not stored.** A row written by an older build must not pin an
// offer this one no longer honours, and a suggestion that has been withdrawn
// should disappear from an existing Issue rather than sit there failing when
// pressed.
//
// Dismiss is on every type, deliberately: "I know, and I am choosing to live
// with it" is a decision, and an Issue nobody can close becomes furniture.
func SuggestionsFor(t IssueType) []SuggestionType {
	switch t {
	case IssueExtensionUnavailable:
		return []SuggestionType{SuggestionReinstallExtension, SuggestionUninstallExtension, SuggestionDismiss}
	case IssueChildUnrecoverable, IssueGenerationRolledBack, IssueProvisionFailed:
		// Nothing this build can *do* about these from a screen — restarting a
		// child and re-running an upgrade are the Supervisor's, and it has no
		// action lane (ADR 0123 gave it a read surface and no more). Offering a
		// control that did nothing would be worse than offering none, so the
		// honest offer is to acknowledge it.
		return []SuggestionType{SuggestionDismiss}
	default:
		return []SuggestionType{SuggestionDismiss}
	}
}
