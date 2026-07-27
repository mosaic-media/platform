// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"strings"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Library rules (ADR 0104) — the durable, administrator-owned statement of what
// the library should contain, and the read that evaluates one.
//
// The evaluation lives here beside the management commands rather than in the
// maintenance job, because a rule has to be *evaluated before it is saved*: the
// first run of a new rule is the one most likely to surprise its author, so the
// surface that creates one says what it will do before it does it. Preview and
// reconcile therefore walk the same code, and a preview that disagreed with the
// run it previewed would be worse than no preview at all.

// ActionLibraryRuleRead is reading what the library should contain.
//
// Its own action rather than `content.read`, and the distinction is real: the
// library is shared and everyone who may see it sees all of it (ADR 0103),
// while the rules are the install's *policy* about the library. Who decided
// that a household owns every episode of something is administrative
// information about the install, not a fact about the content.
const ActionLibraryRuleRead policy.Action = "library.rule.read"

// ActionLibraryRuleManage is writing one — creating, enabling, disabling or
// deleting.
const ActionLibraryRuleManage policy.Action = "library.rule.manage"

// LibraryRuleListing is one rule as a management surface needs it: the durable
// statement, and whether the install can act on it right now.
type LibraryRuleListing struct {
	Rule domain.LibraryRule
	// Available is whether the module the rule names is registered and fills
	// the role the rule needs.
	//
	// **A rule survives its module being uninstalled** (ADR 0104): the row
	// stays, and this is how the surface says so. It is computed per read
	// rather than stored, because "is that extension installed" is a fact about
	// now and a stored copy would be wrong from the moment somebody removed
	// one — the failure being a rule that reads as healthy and does nothing.
	Available bool
}

// ListLibraryRulesQuery lists what the library should contain.
type ListLibraryRulesQuery struct {
	Caller v1.Caller
	// EnabledOnly narrows to the rules the maintenance job would evaluate. The
	// settings surface lists everything; the job asks for this.
	EnabledOnly bool
}

// ListLibraryRulesResult carries the rules, each marked available or degraded.
type ListLibraryRulesResult struct {
	Rules []LibraryRuleListing
}

// ListLibraryRules reads the rules, marking each with whether its module is
// currently there to answer it.
func (s *Service) ListLibraryRules(ctx context.Context, q ListLibraryRulesQuery) (ListLibraryRulesResult, error) {
	if q.Caller.Session == "" {
		return ListLibraryRulesResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if _, err := s.enter(ctx, q.Caller, ActionLibraryRuleRead, policy.Resource{Type: "library_rule"}); err != nil {
		return ListLibraryRulesResult{}, err
	}
	if s.libraryRules == nil {
		// No store is no rules, not an error: a build without one has never
		// been told what its library should contain, which is the same answer
		// as a build that has been told nothing.
		return ListLibraryRulesResult{}, nil
	}
	rules, err := s.libraryRules.List(ctx, domain.LibraryRuleFilter{EnabledOnly: q.EnabledOnly})
	if err != nil {
		return ListLibraryRulesResult{}, err
	}
	out := make([]LibraryRuleListing, 0, len(rules))
	for _, rule := range rules {
		out = append(out, LibraryRuleListing{Rule: rule, Available: s.ruleModuleAvailable(rule)})
	}
	return ListLibraryRulesResult{Rules: out}, nil
}

// ruleModuleAvailable reports whether the module a rule names is registered and
// fills the role that rule needs. A collection rule needs a catalog provider; a
// query rule needs a search provider. A module that is installed but fills the
// wrong role is as unusable as one that is gone, and reads the same way here.
func (s *Service) ruleModuleAvailable(rule domain.LibraryRule) bool {
	if s.capabilities == nil {
		return false
	}
	switch rule.Kind {
	case domain.LibraryRuleCollection:
		_, ok := s.capabilities.CatalogProvider(rule.ModuleID)
		return ok
	case domain.LibraryRuleQuery:
		_, ok := s.capabilities.SearchProvider(rule.ModuleID)
		return ok
	default:
		return false
	}
}

// CreateLibraryRuleCommand writes a new statement of what the library should
// contain.
type CreateLibraryRuleCommand struct {
	Caller     v1.Caller
	Name       string
	Kind       domain.LibraryRuleKind
	ModuleID   string
	CatalogID  string
	NativeType string
	Text       string
	MediaType  string
	Bound      int
}

// CreateLibraryRuleResult carries the stored rule.
type CreateLibraryRuleResult struct {
	Rule domain.LibraryRule
}

// CreateLibraryRule stores a rule.
//
// It deliberately does **not** evaluate the rule as a side effect of creating
// it. Materialising a hundred titles because somebody pressed Save is the
// surprise ADR 0104 names, and the honest arrangement is that saving states an
// intention and a run acts on it — with a preview in between, which is what
// PreviewLibraryRule is for.
func (s *Service) CreateLibraryRule(ctx context.Context, cmd CreateLibraryRuleCommand) (CreateLibraryRuleResult, error) {
	// 1. validate command shape.
	rule, err := s.validateNewLibraryRule(cmd)
	if err != nil {
		return CreateLibraryRuleResult{}, err
	}

	// 2-3. authenticate the caller and authorize the action.
	az, err := s.enter(ctx, cmd.Caller, ActionLibraryRuleManage, policy.Resource{Type: "library_rule"})
	if err != nil {
		return CreateLibraryRuleResult{}, err
	}

	now := s.clock.Now()
	rule.ID = domain.LibraryRuleID(s.ids.NewID())
	rule.Enabled = true
	rule.CreatedBy = az.userID
	rule.CreatedAt = now
	rule.UpdatedAt = now

	var stored domain.LibraryRule
	// 4-7. one transaction for the rule and the event announcing it.
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		created, err := tx.LibraryRules().Create(ctx, rule)
		if err != nil {
			return err
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "library.rule.created", []byte(created.Name), string(az.userID)),
		}); err != nil {
			return err
		}
		stored = created
		return nil
	})
	if err != nil {
		return CreateLibraryRuleResult{}, err
	}
	return CreateLibraryRuleResult{Rule: stored}, nil
}

// validateNewLibraryRule checks a proposed rule and returns it trimmed.
//
// It is shared with the preview, which is what makes "say what it will do
// before it does it" possible: a preview of a rule that would be refused on
// save is a preview of nothing.
func (s *Service) validateNewLibraryRule(cmd CreateLibraryRuleCommand) (domain.LibraryRule, error) {
	if cmd.Caller.Session == "" {
		return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	rule := domain.LibraryRule{
		Name:       cmd.Name,
		Kind:       cmd.Kind,
		ModuleID:   cmd.ModuleID,
		CatalogID:  cmd.CatalogID,
		NativeType: cmd.NativeType,
		Text:       cmd.Text,
		// Canonicalised with the content vocabulary's own function, so a rule
		// saying "Anime Series" narrows to the nodes stored as anime_series
		// rather than to nothing at all (ADR 0015).
		MediaType: string(v1.NormaliseMediaType(cmd.MediaType)),
		Bound:     cmd.Bound,
	}.Trimmed()

	if rule.Name == "" {
		return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument, "a library rule needs a name")
	}
	if rule.ModuleID == "" {
		return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument, "a library rule needs a module")
	}
	if rule.Bound < 0 || rule.Bound > domain.MaxLibraryRuleBound {
		return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument,
			"a library rule's bound must be between 0 and 500, where 0 takes the default")
	}
	switch rule.Kind {
	case domain.LibraryRuleCollection:
		if rule.CatalogID == "" {
			return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument,
				"a collection rule needs a catalog")
		}
	case domain.LibraryRuleQuery:
		if rule.Text == "" {
			return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument,
				"a saved search needs some text to search for")
		}
	default:
		// The kinds are closed and there are two of them (ADR 0104). A rule
		// over the library's own contents is a view, not a source.
		return domain.LibraryRule{}, contracts.NewError(contracts.InvalidArgument,
			"a library rule is either a collection or a saved search")
	}
	return rule, nil
}

// SetLibraryRuleEnabledCommand turns a rule on or off.
type SetLibraryRuleEnabledCommand struct {
	Caller  v1.Caller
	RuleID  domain.LibraryRuleID
	Enabled bool
}

// SetLibraryRuleEnabled stops or resumes a rule without discarding it.
//
// It exists because the alternative to turning a rule off is deleting it, and
// deleting is how a statement of intent gets lost: a source that became
// expensive, or a catalog that started returning the wrong thing, is a reason to
// stop adding and not a reason to forget what was asked for. Disabling never
// removes anything the rule already materialised — rules add and do not remove,
// and that holds when the rule stops running.
func (s *Service) SetLibraryRuleEnabled(ctx context.Context, cmd SetLibraryRuleEnabledCommand) (CreateLibraryRuleResult, error) {
	if cmd.Caller.Session == "" {
		return CreateLibraryRuleResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.RuleID == "" {
		return CreateLibraryRuleResult{}, contracts.NewError(contracts.InvalidArgument, "a rule id is required")
	}
	az, err := s.enter(ctx, cmd.Caller, ActionLibraryRuleManage, policy.Resource{
		Type: "library_rule", ID: string(cmd.RuleID),
	})
	if err != nil {
		return CreateLibraryRuleResult{}, err
	}
	if s.libraryRules == nil {
		return CreateLibraryRuleResult{}, contracts.NewError(contracts.Unavailable, "this Platform stores no library rules")
	}

	var stored domain.LibraryRule
	err = s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		rule, err := tx.LibraryRules().FindByID(ctx, cmd.RuleID)
		if err != nil {
			return err
		}
		rule.Enabled = cmd.Enabled
		rule.UpdatedAt = s.clock.Now()
		updated, err := tx.LibraryRules().Update(ctx, rule)
		if err != nil {
			return err
		}
		event := "library.rule.disabled"
		if cmd.Enabled {
			event = "library.rule.enabled"
		}
		if err := tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, event, []byte(updated.Name), string(az.userID)),
		}); err != nil {
			return err
		}
		stored = updated
		return nil
	})
	if err != nil {
		return CreateLibraryRuleResult{}, err
	}
	return CreateLibraryRuleResult{Rule: stored}, nil
}

// DeleteLibraryRuleCommand withdraws a statement.
type DeleteLibraryRuleCommand struct {
	Caller v1.Caller
	RuleID domain.LibraryRuleID
}

// DeleteLibraryRule removes the rule and **nothing it materialised**.
//
// That is the whole shape of ADR 0104's "rules add and never remove", applied to
// the rule itself. Deleting a rule that had pulled in two hundred titles must
// not delete two hundred titles: a source's churn is not a household's decision,
// and neither is an administrator tidying up their rules.
func (s *Service) DeleteLibraryRule(ctx context.Context, cmd DeleteLibraryRuleCommand) error {
	if cmd.Caller.Session == "" {
		return contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if cmd.RuleID == "" {
		return contracts.NewError(contracts.InvalidArgument, "a rule id is required")
	}
	az, err := s.enter(ctx, cmd.Caller, ActionLibraryRuleManage, policy.Resource{
		Type: "library_rule", ID: string(cmd.RuleID),
	})
	if err != nil {
		return err
	}
	if s.libraryRules == nil {
		return contracts.NewError(contracts.Unavailable, "this Platform stores no library rules")
	}
	return s.uow.WithinTx(ctx, func(ctx context.Context, tx contracts.Tx) error {
		if err := tx.LibraryRules().Delete(ctx, cmd.RuleID); err != nil {
			return err
		}
		return tx.Outbox().Append(ctx, domain.OutboxEvent{
			Event: s.newEvent(ctx, "library.rule.deleted", []byte(cmd.RuleID), string(az.userID)),
		})
	})
}

// PreviewLibraryRuleQuery asks what a rule would do.
//
// It takes the rule's fields rather than an id, because the moment it is most
// needed is **before the rule exists**: an author about to save one should be
// told how many titles it will pull in and how many of them the library already
// has. Passing RuleID instead previews a stored rule, which is the same question
// asked later.
type PreviewLibraryRuleQuery struct {
	Caller v1.Caller
	RuleID domain.LibraryRuleID

	Name       string
	Kind       domain.LibraryRuleKind
	ModuleID   string
	CatalogID  string
	NativeType string
	Text       string
	MediaType  string
	Bound      int
}

// PreviewLibraryRuleResult is what the first run would do.
type PreviewLibraryRuleResult struct {
	// Matched is how many items the source offered within the bound.
	Matched int
	// AlreadyInLibrary is how many of those the library already holds. It is
	// the number that turns "this will add 100 titles" into "this will add 12",
	// which is usually the difference between alarming and reassuring.
	AlreadyInLibrary int
	// WouldAdd is Matched minus AlreadyInLibrary — what the first run creates.
	WouldAdd int
	// Bound is the limit that was applied, so the reader can tell "40 titles"
	// from "the first 40 of an unknown number".
	Bound int
	// Truncated is whether the source had more to give at the bound.
	Truncated bool
	// Sample is the first few titles, so the author can see whether the rule
	// says what they meant. A count alone cannot show that a catalog id was
	// mistyped into somebody else's collection.
	Sample []string
}

// previewSampleSize is how many titles a preview shows by name. Enough to
// recognise the collection, few enough to read at a glance.
const previewSampleSize = 6

// PreviewLibraryRule evaluates a rule without writing anything.
func (s *Service) PreviewLibraryRule(ctx context.Context, q PreviewLibraryRuleQuery) (PreviewLibraryRuleResult, error) {
	if q.Caller.Session == "" {
		return PreviewLibraryRuleResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}

	// Reading a source on behalf of a rule is the rule-management authority,
	// not the content-read one: it is how an administrator inspects a statement
	// they are about to make about the install.
	az, err := s.enter(ctx, q.Caller, ActionLibraryRuleManage, policy.Resource{Type: "library_rule"})
	if err != nil {
		return PreviewLibraryRuleResult{}, err
	}

	rule, err := s.previewSubject(ctx, q)
	if err != nil {
		return PreviewLibraryRuleResult{}, err
	}

	// Previewed as the person asking, not as the system principal. The run acts
	// as the install because its writes are the install's (ADR 0017); a preview
	// writes nothing and is somebody looking at a source, so it is theirs.
	matches, truncated, err := s.evaluateLibraryRule(ctx, az, q.Caller, rule)
	if err != nil {
		return PreviewLibraryRuleResult{}, err
	}

	out := PreviewLibraryRuleResult{
		Matched:   len(matches),
		Bound:     rule.EffectiveBound(),
		Truncated: truncated,
	}
	for _, match := range matches {
		if match.InLibrary {
			out.AlreadyInLibrary++
		}
		if len(out.Sample) < previewSampleSize && match.Title != "" {
			out.Sample = append(out.Sample, match.Title)
		}
	}
	out.WouldAdd = out.Matched - out.AlreadyInLibrary
	return out, nil
}

// previewSubject resolves which rule a preview is about: a stored one when the
// query names an id, otherwise the proposed one the query carries, validated
// exactly as CreateLibraryRule would validate it.
func (s *Service) previewSubject(ctx context.Context, q PreviewLibraryRuleQuery) (domain.LibraryRule, error) {
	if q.RuleID != "" {
		if s.libraryRules == nil {
			return domain.LibraryRule{}, contracts.NewError(contracts.Unavailable, "this Platform stores no library rules")
		}
		return s.libraryRules.FindByID(ctx, q.RuleID)
	}
	return s.validateNewLibraryRule(CreateLibraryRuleCommand{
		Caller: q.Caller, Name: q.Name, Kind: q.Kind, ModuleID: q.ModuleID,
		CatalogID: q.CatalogID, NativeType: q.NativeType, Text: q.Text,
		MediaType: q.MediaType, Bound: q.Bound,
	})
}

// libraryRuleMatch is one item a rule matched: the ref to materialise, the
// title to show, and whether the library already holds it.
type libraryRuleMatch struct {
	Ref       v1.ContentRef
	Title     string
	InLibrary bool
}

// evaluateLibraryRule reads a rule's source and returns what it matched, up to
// the rule's bound. It writes nothing.
//
// **One implementation for the preview and the run**, which is the property that
// makes a preview worth showing. It reports truncation separately from the match
// count so a caller can say "the first 40 of more" rather than "40", which is
// the difference between a bound and a total.
func (s *Service) evaluateLibraryRule(ctx context.Context, az authorized, caller v1.Caller, rule domain.LibraryRule) ([]libraryRuleMatch, bool, error) {
	if !s.ruleModuleAvailable(rule) {
		// Degraded, and said so rather than returning an empty match set. A
		// rule whose module is gone matching nothing and a rule whose catalog
		// is empty matching nothing are different facts, and only one of them
		// is something an administrator can fix.
		return nil, false, contracts.NewError(contracts.NotFound,
			"the module this rule names ("+rule.ModuleID+") is not installed, or no longer does what the rule needs")
	}
	switch rule.Kind {
	case domain.LibraryRuleCollection:
		return s.evaluateCollectionRule(ctx, az, caller, rule)
	case domain.LibraryRuleQuery:
		return s.evaluateQueryRule(ctx, az, caller, rule)
	default:
		return nil, false, contracts.NewError(contracts.InvalidArgument, "unknown library rule kind "+string(rule.Kind))
	}
}

// evaluateCollectionRule pages a module catalog up to the rule's bound.
func (s *Service) evaluateCollectionRule(ctx context.Context, az authorized, caller v1.Caller, rule domain.LibraryRule) ([]libraryRuleMatch, bool, error) {
	bound := rule.EffectiveBound()
	var matches []libraryRuleMatch
	truncated := false

	for len(matches) < bound {
		page, err := s.catalogItemsPage(ctx, az, ListCatalogItemsQuery{
			Caller: caller, ModuleID: rule.ModuleID, CatalogID: rule.CatalogID,
			NativeType: rule.NativeType, Skip: len(matches),
		})
		if err != nil {
			// A source that fails on the first page fails the rule; one that
			// fails partway keeps what it gave. A partial evaluation is a
			// legitimate outcome for a pass that adds and never removes.
			if len(matches) == 0 {
				return nil, false, err
			}
			return matches, true, nil
		}
		if len(page.Items) == 0 {
			// A provider that returned nothing has nothing further, whatever it
			// says about HasMore — the same guard the catalog screen carries,
			// and without it a wrong HasMore is an unbounded loop over an empty
			// page.
			break
		}
		for _, item := range page.Items {
			if len(matches) >= bound {
				truncated = true
				break
			}
			matches = append(matches, libraryRuleMatch{
				Ref: item.Ref, Title: item.Title, InLibrary: item.InLibrary,
			})
		}
		if !page.HasMore {
			break
		}
		if len(matches) >= bound {
			truncated = true
		}
	}
	return matches, truncated, nil
}

// evaluateQueryRule runs the saved search against the one module it names.
func (s *Service) evaluateQueryRule(ctx context.Context, az authorized, caller v1.Caller, rule domain.LibraryRule) ([]libraryRuleMatch, bool, error) {
	provider, ok := s.capabilities.SearchProvider(rule.ModuleID)
	if !ok {
		return nil, false, contracts.NewError(contracts.NotFound, "no search provider registered under id "+rule.ModuleID)
	}
	settings, err := s.readModuleSettings(ctx, rule.ModuleID)
	if err != nil {
		return nil, false, err
	}

	bound := rule.EffectiveBound()
	mctx, span := moduleSpan(ctx, rule.ModuleID, "search")
	resp, err := provider.Search(mctx, v1.SearchRequest{
		Caller: caller, Settings: settings, Text: rule.Text,
		MediaType: v1.MediaType(rule.MediaType), Limit: bound,
	})
	failSpan(span, err)
	span.End()
	if err != nil {
		return nil, false, contracts.WrapError(contracts.Unavailable, "evaluate saved search", err)
	}

	// A provider is handed a limit and is not obliged to honour it, so the
	// bound is applied here as well. A rule that says "forty" must mean forty
	// whatever the source returns.
	results := resp.Results
	truncated := false
	if len(results) > bound {
		results = results[:bound]
		truncated = true
	}
	matches := make([]libraryRuleMatch, 0, len(results))
	for _, result := range results {
		inLibrary, _ := s.resolveInLibrary(ctx, az, result.Ref)
		matches = append(matches, libraryRuleMatch{Ref: result.Ref, Title: result.Title, InLibrary: inLibrary})
	}
	return matches, truncated, nil
}

// describeLibraryRule is the one line a log or a surface uses to say which rule
// it means. Kept here so the run log and the settings panel say the same thing.
func describeLibraryRule(rule domain.LibraryRule) string {
	switch rule.Kind {
	case domain.LibraryRuleCollection:
		parts := []string{rule.ModuleID, rule.CatalogID}
		if rule.NativeType != "" {
			parts = append(parts, rule.NativeType)
		}
		return strings.Join(parts, " · ")
	case domain.LibraryRuleQuery:
		out := rule.ModuleID + " · “" + rule.Text + "”"
		if rule.MediaType != "" {
			out += " · " + rule.MediaType
		}
		return out
	default:
		return rule.ModuleID
	}
}

// logLibraryRule records one rule's outcome in the Platform log, beside the job
// log line the run writes. Two records rather than one because they answer to
// different readers: this one joins the trace waterfall, and the job log is
// what an administrator reads on the rule.
func logLibraryRule(ctx context.Context, rule domain.LibraryRule, run domain.LibraryRuleRun) {
	telemetry.From(ctx).For("library").Info("library rule evaluated",
		telemetry.String("rule", rule.Name),
		telemetry.String("kind", string(rule.Kind)),
		telemetry.String("module", rule.ModuleID),
		telemetry.Int("matched", run.Matched),
		telemetry.Int("created", run.Created),
		telemetry.Int("refreshed", run.Refreshed),
		telemetry.Int("skipped", run.Skipped),
		telemetry.Int("failed", run.Failed))
}
