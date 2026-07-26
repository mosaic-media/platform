// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"
	"fmt"
	"strings"

	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/domain"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// The People section: the doors on multi-user (ADR 0069, roadmap M1.3).
//
// Every command behind these panels — CreateLocalUser, ListUsers, GetUserByID,
// SetUserStatus, CreateRole, GrantRole, GetRolesForUser, GetGrantsForUser,
// GetEffectivePermissions — was complete, tested, transactional and reachable
// by nobody. This file is the doors.
//
// **The offer is computed from what the caller holds** (ADR 0069). A grantor
// never sees a permission they do not have — not greyed out, absent — because
// the list comes from their own grants rather than from a filter applied to
// every permission the Platform knows. That is the offer side; CreateRole's own
// delegation check is what actually holds, because the command surface is
// reachable without going through any screen at all.

// The People panels' params and actions.
const (
	// paramUserID names which account a panel is about.
	paramUserID = "userId"
	// paramPreset names which starting selection a new-account form is filling
	// in. It is a param rather than a field on the form because changing it
	// changes what the form *offers*, which only the server can recompute — a
	// select the client could change would leave the permission list beside it
	// describing the preset that was chosen a moment ago.
	paramPreset = "preset"
	// paramNew opens the new-account form rather than a person.
	paramNew = "new"

	createAccountAction  = "createAccount"
	setUserStatusAction  = "setUserStatus"
	grantPresetAction    = "grantPreset"
	fieldAccountUsername = "username"
	fieldAccountName     = "displayName"
	fieldAccountEmail    = "email"
	fieldAccountPassword = "password"
)

// peoplePanel lists who can reach this server and what they are.
//
// The design's per-person "last seen" is still absent: sessions are listed per
// caller and there is no read that answers it for somebody else. What is here
// is real — the accounts, their roles, whether each is active, and a way into
// each one.
func (s *Service) peoplePanel(ctx context.Context, caller v1.Caller, nav settingsNavModel, params map[string]any) (sdui.Node, error) {
	if preset := stringParam(params, paramNew); preset != "" {
		return s.newAccountPanel(ctx, caller, nav, preset)
	}
	if userID := stringParam(params, paramUserID); userID != "" {
		return s.personPanel(ctx, caller, nav, domain.UserID(userID))
	}

	res, err := s.content.ListUsers(ctx, app.ListUsersQuery{
		CallerSessionID: domain.SessionID(caller.Session),
	})
	if err != nil {
		return nil, err
	}

	body := []sdui.Node{}
	if len(res.Users) == 0 {
		body = append(body, ui.EmptyState(emptyIconCollections, "No accounts on this server").Build())
	} else {
		rows := make([]ui.El, 0, len(res.Users))
		for _, u := range res.Users {
			// The way in is a control in the row's own slot, not an action on
			// the row. **A SettingsRow reads no `action`**, so setting one
			// compiled, rendered, changed nothing and reported nothing — the
			// props bag accepts anything, which is how ui.Subtitle came to be
			// set on a Stack for the whole life of a screen. The Devices
			// section already had the right idiom and this now matches it.
			rows = append(rows, ui.SettingsRow(displayNameOf(u),
				ui.Summary(personSummary(s.rolesFor(ctx, caller, u.ID), u.Username)),
				ui.Value(titleWords(string(u.Status))),
				ui.Group(ui.Button("Manage", "quiet",
					ui.OnTap(ui.Navigate(screenSettings, map[string]any{
						paramSection: sectionPeople,
						paramUserID:  string(u.ID),
					}))))))
		}
		body = append(body, ui.Stack("vertical", 0, rows...).Build())
	}

	if add := s.addAccountSection(ctx, caller); add != nil {
		body = append(body, add.Build())
	}

	return settingsFrame(nav, sectionPeople, "People",
		fmt.Sprintf("%d %s on this server, and what each is allowed to do.",
			len(res.Users), plural(len(res.Users), "account")),
		body...,
	), nil
}

// addAccountSection offers the presets this caller can actually start from, or
// nothing at all.
//
// It asks the Platform which presets it may confer rather than listing the
// three it knows about, so an administrator who cannot create roles sees no
// invitation to try. A control that leads to a form whose submit will be
// refused is the dead end ADR 0036 exists to remove, and it is worse on this
// screen than anywhere else: being told you may not create an account *after*
// typing somebody's password in is the version of that failure people remember.
func (s *Service) addAccountSection(ctx context.Context, caller v1.Caller) *ui.Element {
	if !s.content.CallerCan(ctx, caller, app.ActionUserCreate, "user") {
		return nil
	}
	if !s.content.CallerCan(ctx, caller, app.ActionRoleCreate, "role") {
		return nil
	}
	buttons := make([]ui.El, 0, 2)
	for _, preset := range offerablePresets {
		if !s.canOffer(ctx, caller, preset) {
			continue
		}
		buttons = append(buttons, ui.Button("Add "+presetArticle(preset), "secondary",
			ui.OnTap(ui.Navigate(screenSettings, map[string]any{
				paramSection: sectionPeople,
				paramNew:     preset,
			}))))
	}
	if len(buttons) == 0 {
		return nil
	}
	return ui.Section("Add someone",
		ui.Stack("horizontal", 3, buttons...))
}

// offerablePresets are the two an administrator starts an account from.
//
// Superuser is deliberately absent. It is the account established out of band —
// the root of every other grant — and a second one is not something a setup
// screen should make easy. Nothing stops a superuser conferring their whole set
// deliberately; what is withheld is the one-tap version of it.
var offerablePresets = []string{app.PresetNameUser, app.PresetNameAdministrator}

// canOffer reports whether this caller could confer any of a preset at all.
func (s *Service) canOffer(ctx context.Context, caller v1.Caller, preset string) bool {
	res, err := s.content.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: caller, Preset: preset,
	})
	return err == nil && len(res.Selected) > 0
}

// presetArticle reads a preset name into the sentence a button is.
func presetArticle(preset string) string {
	if preset == app.PresetNameAdministrator {
		return "an administrator"
	}
	return "a viewer"
}

// newAccountPanel is the create-account form.
//
// **What it will grant is shown before anything is typed**, as the exact set
// the Platform computed for this grantor. Two people opening this form can
// legitimately see different lists, because the list is their own authority
// narrowed by the preset; a form that showed the preset's full set and then
// silently conferred less would be lying about the thing it is for.
func (s *Service) newAccountPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel, preset string) (sdui.Node, error) {
	offer, err := s.content.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: caller, Preset: preset,
	})
	if err != nil {
		return nil, err
	}

	granted := make([]ui.El, 0, len(offer.Selected))
	for _, p := range offer.Selected {
		granted = append(granted, ui.Badge(string(p), ui.ToneNeutral))
	}

	// What the preset asks for and this grantor cannot give. Named rather than
	// omitted: an administrator creating another administrator should be able to
	// see that the account will be narrower than their own idea of the word,
	// which is the whole consequence of ADR 0069's intersection and is otherwise
	// invisible.
	var withheld []ui.El
	if full, ok := app.Preset(preset); ok {
		held := make(map[string]bool, len(offer.Selected))
		for _, p := range offer.Selected {
			held[string(p)] = true
		}
		for _, a := range full {
			if !held[string(a)] {
				withheld = append(withheld, ui.Badge(string(a), ui.ToneWarning))
			}
		}
	}

	body := []sdui.Node{
		ui.State(
			ui.Vars(sdui.Vars(
				sdui.Var(fieldAccountName, sdui.VarString, ""),
				sdui.Var(fieldAccountUsername, sdui.VarString, ""),
				sdui.Var(fieldAccountEmail, sdui.VarString, ""),
				sdui.Var(fieldAccountPassword, sdui.VarString, ""),
				sdui.Var(fieldFormError, sdui.VarString, ""),
			)),
			ui.Form(
				ui.BindError(fieldFormError),
				ui.SubmitLabel("Create account"),
				// The preset travels in the action rather than in the scope,
				// because it is not something this form collects — it is which
				// form this is. ADR 0096's merge rule is what makes that safe:
				// the scope wins only for the names it declares, and this is not
				// one of them.
				ui.SubmitAction(ui.Submit(ui.Invoke(createAccountAction, map[string]any{
					paramPreset: preset,
				}), "")),
				ui.TextField("Name", ui.Name(fieldAccountName), ui.Placeholder("Sam")),
				ui.TextField("Username",
					ui.Name(fieldAccountUsername),
					ui.Placeholder("sam"),
					ui.Help("What they sign in with. No spaces."),
					ui.Validators(map[string]any{"required": true})),
				ui.TextField("Email",
					ui.Name(fieldAccountEmail),
					ui.InputType("email"),
					ui.Help("Only used to tell accounts apart. Mosaic sends no mail.")),
				ui.TextField("Password",
					ui.Name(fieldAccountPassword),
					ui.InputType("password"),
					ui.Help("At least 8 characters. Tell them; they can be given a new one later."),
					ui.Validators(map[string]any{"required": true, "minLength": 8})),
			),
		).Build(),
		ui.Section("What this account will be able to do",
			ui.Stack("horizontal", 2, granted...)).Build(),
	}
	if len(withheld) > 0 {
		body = append(body, ui.Section("Not included, because you do not hold it",
			ui.Banner("Nobody can grant authority they do not have themselves, so these are left out "+
				"of the account you are about to create.", ui.ToneInfo),
			ui.Stack("horizontal", 2, withheld...)).Build())
	}
	body = append(body, backToPeople().Build())

	return settingsFrame(nav, sectionPeople, "Add "+presetArticle(preset),
		"They will be able to sign in on their own device, with their own progress and history.",
		body...,
	), nil
}

// personPanel is one account: who they are, what they hold, and the two things
// that can be done to them.
func (s *Service) personPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel, userID domain.UserID) (sdui.Node, error) {
	res, err := s.content.GetUserByID(ctx, app.GetUserByIDQuery{
		CallerSessionID: domain.SessionID(caller.Session),
		UserID:          userID,
	})
	if err != nil {
		return nil, err
	}
	u := res.User

	rows := []ui.El{
		ui.SettingsRow("Username", ui.Value(u.Username)),
	}
	if u.Email != "" {
		rows = append(rows, ui.SettingsRow("Email", ui.Value(u.Email)))
	}
	rows = append(rows, ui.SettingsRow("Status", ui.Value(titleWords(string(u.Status)))))
	if !u.CreatedAt.IsZero() {
		rows = append(rows, ui.SettingsRow("Created", ui.Value(u.CreatedAt.Format("2 January 2006"))))
	}

	body := []sdui.Node{ui.Stack("vertical", 0, rows...).Build()}
	if authority := s.authoritySection(ctx, caller, u.ID); authority != nil {
		body = append(body, authority.Build())
	}
	if status := s.statusSection(ctx, caller, u); status != nil {
		body = append(body, status.Build())
	}
	body = append(body, backToPeople().Build())

	return settingsFrame(nav, sectionPeople, displayNameOf(u),
		"One account on this server.", body...), nil
}

// authoritySection says what an account may actually do, and offers to widen it.
//
// Roles and effective permissions are both shown because they answer different
// questions: the roles are what somebody was given, and the flattened set is
// what the policy engine will decide with. They come apart the moment an
// account holds two roles, and the second is the one that is true.
func (s *Service) authoritySection(ctx context.Context, caller v1.Caller, userID domain.UserID) *ui.Element {
	if !s.content.CallerCan(ctx, caller, app.ActionPermissionRead, "user") {
		return nil
	}

	els := []ui.El{}
	roles, err := s.content.GetRolesForUser(ctx, app.GetRolesForUserQuery{
		CallerSessionID: domain.SessionID(caller.Session), TargetUserID: userID,
	})
	switch {
	case err != nil:
		return nil
	case len(roles.Roles) == 0:
		// An account with no role cannot sign in: authenticating is itself
		// policy-gated, so somebody holding nothing is refused a session. That
		// is a state a half-finished creation can leave behind, so it is named
		// here rather than left as a mystery.
		els = append(els, ui.Banner("This account holds no role, so it cannot sign in yet. "+
			"Give it one below.", ui.ToneWarning))
	default:
		names := make([]ui.El, 0, len(roles.Roles))
		for _, r := range roles.Roles {
			names = append(names, ui.Badge(r.Name, ui.ToneInfo))
		}
		els = append(els, ui.Stack("horizontal", 2, names...))
	}

	if perms, err := s.content.GetEffectivePermissions(ctx, app.GetEffectivePermissionsQuery{
		CallerSessionID: domain.SessionID(caller.Session), TargetUserID: userID,
	}); err == nil && len(perms.Permissions) > 0 {
		badges := make([]ui.El, 0, len(perms.Permissions))
		for _, p := range perms.Permissions {
			badges = append(badges, ui.Badge(string(p), ui.ToneNeutral))
		}
		els = append(els,
			ui.SettingsRow("Effective permissions", ui.Value(fmt.Sprint(len(perms.Permissions)))),
			ui.Stack("horizontal", 2, badges...))
	}

	if grants := s.grantButtons(ctx, caller, userID); len(grants) > 0 {
		els = append(els, ui.Stack("horizontal", 3, grants...))
	}
	return ui.Section("Authority", els...)
}

// grantButtons offers to add a preset's worth of authority to an account.
//
// This is the recovery path as much as it is a feature: creating an account is
// three commands and they do not share a transaction, so a failure between them
// leaves a user with no role. Rather than hide that, the panel says so and
// offers the step that was missed.
func (s *Service) grantButtons(ctx context.Context, caller v1.Caller, userID domain.UserID) []ui.El {
	if !s.content.CallerCan(ctx, caller, app.ActionRoleGrant, "role") {
		return nil
	}
	out := make([]ui.El, 0, len(offerablePresets))
	for _, preset := range offerablePresets {
		if !s.canOffer(ctx, caller, preset) {
			continue
		}
		out = append(out, ui.Button("Grant "+preset, "quiet",
			ui.OnTap(ui.Invoke(grantPresetAction, map[string]any{
				paramUserID: string(userID),
				paramPreset: preset,
			}))))
	}
	return out
}

// statusSection is suspend and reactivate.
//
// A person cannot suspend themselves. It is not an authority question — an
// administrator plainly may — but the outcome of the one install with a single
// administrator doing it is a server nobody can administer, and there is no
// recovery command for that. So the control is absent for your own row, with
// the reason stated rather than the button greyed out.
func (s *Service) statusSection(ctx context.Context, caller v1.Caller, u domain.User) *ui.Element {
	if !s.content.CallerCan(ctx, caller, app.ActionUserStatusUpdate, "user") {
		return nil
	}
	if me, err := s.currentUser(ctx, caller); err == nil && me.User.ID == u.ID {
		return ui.Section("Access",
			ui.Banner("This is the account you are signed in as. Suspending it from here would "+
				"lock you out of the server with nothing left to unlock it with.", ui.ToneInfo))
	}

	if u.Status == domain.UserSuspended {
		return ui.Section("Access",
			ui.Banner("This account is suspended: its password still works and it is refused a "+
				"session, so nothing it watched has been lost.", ui.ToneWarning),
			ui.Button("Reactivate", "secondary", ui.OnTap(ui.Invoke(setUserStatusAction, map[string]any{
				paramUserID: string(u.ID),
				paramStatus: string(domain.UserActive),
			}))))
	}
	return ui.Section("Access",
		ui.Banner("Suspending stops this account signing in. It keeps its progress, its history "+
			"and its place in the library — nothing is deleted, and reactivating restores it "+
			"exactly.", ui.ToneInfo),
		ui.Button("Suspend", "dangerQuiet", ui.OnTap(ui.Invoke(setUserStatusAction, map[string]any{
			paramUserID: string(u.ID),
			paramStatus: string(domain.UserSuspended),
		}))))
}

// backToPeople is the way out of a panel that drilled in.
//
// The nav never left the screen, so this is not the only way back — it is the
// one that returns to the list rather than to a section, which is what somebody
// who has just created an account is looking for.
func backToPeople() *ui.Element {
	return ui.Button("Back to people", "ghost",
		ui.OnTap(ui.Navigate(screenSettings, map[string]any{paramSection: sectionPeople})))
}

// displayNameOf is what to call an account: the name it chose, and its username
// when it has not chosen one.
func displayNameOf(u domain.User) string {
	if strings.TrimSpace(u.DisplayName) != "" {
		return u.DisplayName
	}
	return u.Username
}

// personSummary is the line under a name in the list.
func personSummary(roles, username string) string {
	if roles == "" {
		return username
	}
	return roles + " · " + username
}
