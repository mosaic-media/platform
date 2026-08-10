// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package domain

import (
	"strings"
	"time"
)

// The library rule (platform#60).
//
// A rule is a durable, administrator-owned statement of what the library
// *should* contain. Nothing in Mosaic said that before: the library was
// whatever individuals happened to press Add on, so nothing could notice when a
// catalog gained a title or a source stopped carrying one.
//
// It is **Platform state and not SDK state**, which is the load-bearing choice
// in this file. A rule references a module the way a screen does — by id — and
// it outlives that module being uninstalled ([platform#51]): the row stays,
// degraded and visibly so, because an extension is removable at runtime and a
// rule that deleted itself with the module would silently unmake a household's
// decision. Nothing here is on the published surface, and no module reads its
// own rules.
//
// [platform#51]: https://github.com/mosaic-media/platform/blob/main/docs/adr/0051-extension-installation-is-user-initiated-and-persistent.md

// LibraryRuleID identifies one rule.
type LibraryRuleID ID

// LibraryRuleKind is which of the two shapes a rule takes. It is a **closed**
// set and deliberately has exactly two members (platform#60): a rule over the
// library's own contents is a view rather than a source, and a rule that reads
// what another rule wrote is a loop with no fixed point. Platform code branches
// on this value to decide which provider role to evaluate it against, which is
// the test for a closed vocabulary (platform#11).
type LibraryRuleKind string

const (
	// LibraryRuleCollection names a module catalog the way the catalog screen
	// addresses one — module id, catalog id, native type.
	LibraryRuleCollection LibraryRuleKind = "collection"
	// LibraryRuleQuery is a saved provider search: the text a user would have
	// typed, against one module's search role.
	LibraryRuleQuery LibraryRuleKind = "query"
)

// DefaultLibraryRuleBound is how many items a rule evaluates when its author
// set no bound.
//
// A bound rather than "all of it", and the reason is in platform#60's
// consequences: a rule can produce a large library quickly, so the first run of
// a new rule is the one most likely to surprise its author. A Stremio-class
// aggregator exposes thousands of titles per catalog, and materialising each is
// a metadata call plus a stream fan-out per item — so an unbounded first run is
// how a household exhausts a shared rate limit before anybody has looked at the
// screen.
const DefaultLibraryRuleBound = 40

// MaxLibraryRuleBound caps what an author may ask for. It is a ceiling on one
// rule rather than on a run: the run has its own budget, and a rule bounded
// higher than the run's budget simply takes several runs to fill, which is the
// correct behaviour and not an error.
const MaxLibraryRuleBound = 500

// LibraryRule is one statement of what the library should contain.
type LibraryRule struct {
	ID LibraryRuleID
	// Name is what the administrator calls it. It is the rule's own name and
	// not derived from the module or the catalog, because two rules over the
	// same catalog with different bounds are a legitimate thing to have and
	// "stremio · top" would name both of them.
	Name string
	Kind LibraryRuleKind
	// ModuleID is which module answers the rule. Held for both kinds: a
	// collection rule addresses that module's catalog, and a query rule is
	// saved against one module's search rather than fanned across every
	// installed source — a saved search that silently changed meaning when an
	// extension was installed would not be a durable statement of anything.
	ModuleID string
	// CatalogID and NativeType address one catalog, for a collection rule.
	// Empty on a query rule.
	CatalogID  string
	NativeType string
	// Text is the saved search text, for a query rule. Empty on a collection
	// rule.
	Text string
	// MediaType optionally narrows a query rule, in the Platform's own
	// canonical vocabulary (platform#11) rather than a provider's native word.
	//
	// It is canonicalised by the application service on the way in, with the
	// same function the node store uses, rather than by this package: the
	// canonical form belongs to the content vocabulary and a second copy of the
	// rule here would be a second answer to what "Anime Series" means.
	MediaType string
	// Bound is how many items one evaluation takes, zero meaning
	// DefaultLibraryRuleBound.
	Bound int
	// Enabled is whether the maintenance job evaluates it. A disabled rule is
	// kept and still listed: turning a rule off is how an administrator stops
	// it adding without discarding what it says, and it is the honest answer
	// for a rule whose source has become expensive or wrong.
	Enabled bool
	// CreatedBy is who wrote it. Recorded for the run log's sake — a library
	// that maintains itself is one whose contents nobody chose item by item, so
	// "who asked for this" is a question the install has to be able to answer —
	// and never read to decide authority: the job runs as the system principal
	// and re-authorises nothing against this field (platform#13).
	CreatedBy UserID
	CreatedAt time.Time
	UpdatedAt time.Time
	// LastRun is the account of the most recent evaluation. It is stored on the
	// rule rather than only in the job log because the person who needs it is
	// the administrator looking at their rules, and the job log lives behind
	// expert mode and `job.read`.
	LastRun LibraryRuleRun
}

// LibraryRuleRun is what one evaluation of one rule did.
//
// Four counts and an error, and every one of them is a different fact:
// something new arrived, something already here was topped up, something was
// deliberately passed over, something went wrong. Collapsing them into
// "succeeded" is what makes a self-maintaining library unaccountable.
type LibraryRuleRun struct {
	// At is when the evaluation finished. Zero means the rule has never run,
	// which a surface must say rather than rendering four zeroes as though the
	// rule had run and found nothing.
	At time.Time
	// Matched is how many items the source offered within the rule's bound —
	// the denominator the other counts are against.
	Matched int
	// Created is how many titles this evaluation added to the library.
	Created int
	// Refreshed is how many were already there and were topped up: artwork
	// re-merged and Parts filled for items that had none (platform#46's pass,
	// which is idempotent).
	Refreshed int
	// Skipped is how many were passed over without being attempted — an item
	// the source described too thinly to materialise, or the run's budget
	// running out mid-rule.
	Skipped int
	// Failed is how many were attempted and did not succeed. Best-effort per
	// item is the rule (platform#60): one failure logs and continues, because a
	// run that produced ninety-nine correct nodes has succeeded.
	Failed int
	// Error is why the whole rule could not be evaluated — its module gone, its
	// source unreachable — as distinct from Failed, which counts items. A rule
	// with an Error and no counts was never asked.
	Error string
}

// NeverRun reports whether this rule has been evaluated at all.
func (r LibraryRuleRun) NeverRun() bool { return r.At.IsZero() }

// EffectiveBound is how many items this rule takes, applying the default and
// the ceiling. A stored value out of range is clamped rather than refused: the
// bound governs a background sweep, and a row that would make the sweep refuse
// to run is worse than one that runs at the ceiling.
func (r LibraryRule) EffectiveBound() int {
	switch {
	case r.Bound <= 0:
		return DefaultLibraryRuleBound
	case r.Bound > MaxLibraryRuleBound:
		return MaxLibraryRuleBound
	default:
		return r.Bound
	}
}

// Trimmed removes the surrounding whitespace from the free text a rule
// carries, so two rules differing only in spacing are one rule. It leaves
// MediaType alone — see that field for why the canonical form is applied a
// layer up.
func (r LibraryRule) Trimmed() LibraryRule {
	r.Name = strings.TrimSpace(r.Name)
	r.ModuleID = strings.TrimSpace(r.ModuleID)
	r.CatalogID = strings.TrimSpace(r.CatalogID)
	r.NativeType = strings.TrimSpace(r.NativeType)
	r.Text = strings.TrimSpace(r.Text)
	return r
}

// LibraryRuleFilter narrows a rule listing.
type LibraryRuleFilter struct {
	// EnabledOnly returns only the rules the maintenance job would evaluate.
	// The settings surface lists everything; the job asks for this.
	EnabledOnly bool
}
