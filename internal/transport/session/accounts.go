// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"
	"strings"
	"unicode"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// Account administration, as a client can reach it (ADR 0069, roadmap M1.3).
//
// Three dispatch cases over six application commands. Everything they call was
// complete before this file existed and none of it had a caller outside a test.

// createAccountEnvelope is what the new-account form submits. The four fields
// come from the form's scope; the preset comes from the action, because it is
// which form this is rather than something the form collects (contracts#19's merge
// rule keeps the two apart).
type createAccountEnvelope struct {
	Username    string `json:"username"`
	DisplayName string `json:"displayName"`
	Email       string `json:"email"`
	Password    string `json:"password"`
	Preset      string `json:"preset"`
}

// createAccount provisions a household member: the account, the role carrying
// what the grantor chose to confer, and the grant binding them.
//
// **Three commands, three transactions, and that is stated rather than hidden.**
// CreateLocalUser, CreateRole and GrantRole each own their boundary — each
// validates, authenticates, authorises and commits with its own outbox event —
// and composing them here means a failure between the first and the third
// leaves an account holding no role. That account cannot sign in (authenticating
// is itself policy-gated, so somebody holding nothing is refused a session), it
// is visible in the list, and the person panel names the state and offers the
// step that was missed. The alternative was a fourth command doing all three
// inside one UnitOfWork, which would have left the three real ones exactly as
// unreachable as they were.
func (h *Handler) createAccount(ctx context.Context, caller v1.Caller, input []byte) error {
	var env createAccountEnvelope
	if err := json.Unmarshal(nonEmpty(input), &env); err != nil {
		return contracts.NewError(contracts.InvalidArgument, "create account: input is not valid JSON")
	}
	if err := validateNewAccount(env); err != nil {
		return err
	}

	session := domain.SessionID(caller.Session)
	// What this grantor may actually confer for the preset they chose
	// (ADR 0069). Asked here rather than trusted from the form: the form is an
	// offer and this is the submission, and nothing stops a submission arriving
	// without one.
	offer, err := h.svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: caller, Preset: env.Preset,
	})
	if err != nil {
		return err
	}
	if len(offer.Selected) == 0 {
		return contracts.NewError(contracts.PermissionDenied,
			"you hold none of the permissions the "+env.Preset+" preset starts from")
	}

	created, err := h.svc.CreateLocalUser(ctx, app.CreateLocalUserCommand{
		CallerSessionID: session,
		Username:        strings.TrimSpace(env.Username),
		Email:           accountEmail(env),
		DisplayName:     strings.TrimSpace(env.DisplayName),
		Password:        env.Password,
	})
	if err != nil {
		// A taken username belongs on the field that carries it, not in a toast
		// floating beside a form with four boxes in it (contracts#13).
		if contracts.CategoryOf(err) == contracts.Conflict {
			return contracts.RejectFields("That account could not be created.",
				contracts.FieldRejection{Field: "username", Message: "That username is already taken."})
		}
		return err
	}

	// The install's role for this preset, made the first time somebody asks for
	// it and carrying a **snapshot** of the preset narrowed to what that first
	// grantor held. Snapshotted deliberately: a role created today does not gain
	// an action the Platform adds tomorrow, which is correct for every role
	// except the install owner's — whose the boot-time reconciliation keeps
	// current, because it is the root of every other grant.
	perms := make([]string, 0, len(offer.Selected))
	for _, p := range offer.Selected {
		perms = append(perms, string(p))
	}
	roleID, err := h.presetRole(ctx, caller, env.Preset, perms)
	if err != nil {
		return h.partialAccount(ctx, created.User, "the role could not be created", err)
	}
	if _, err := h.svc.GrantRole(ctx, app.GrantRoleCommand{
		CallerSessionID: session, UserID: created.User.ID, RoleID: roleID,
	}); err != nil {
		return h.partialAccount(ctx, created.User, "the role could not be granted", err)
	}
	return nil
}

// presetRole finds the install's role for a preset, creating it the first time.
//
// **A role name is unique across the install**, which makes "create the User
// role" something that can happen exactly once. Creating one per account was
// the first attempt and it produced three accounts out of four holding no
// authority at all — each one a Conflict on the second insert, each one an
// account that could not sign in. The browser found it; nothing else could,
// because the fakes had no unique index.
//
// So a preset role is an install-wide named role that several people hold. That
// does not weaken the snapshot the roadmap asks for: the snapshot belongs to the
// role, taken when the role was created, and nothing widens it afterwards — an
// account provisioned today still never gains an action added tomorrow.
//
// The set is only used when the role is being created. An existing one is
// granted as it stands, bounded by what the grantor holds (ADR 0069), so a
// reduced administrator granting a wider existing role is refused with the
// permissions named rather than silently handed a narrower one.
func (h *Handler) presetRole(ctx context.Context, caller v1.Caller, preset string, perms []string) (domain.RoleID, error) {
	existing, err := h.svc.GetRoleByName(ctx, app.GetRoleByNameQuery{Caller: caller, Name: preset})
	if err != nil {
		return "", err
	}
	if existing.Found {
		return existing.Role.ID, nil
	}
	created, err := h.svc.CreateRole(ctx, app.CreateRoleCommand{
		CallerSessionID: domain.SessionID(caller.Session), Name: preset, Permissions: perms,
	})
	if err != nil {
		// Two administrators provisioning at once both find nothing and both
		// insert; the unique index makes exactly one of them the winner, and the
		// loser reads what the winner made rather than failing an account over a
		// race that resolved correctly.
		if contracts.CategoryOf(err) == contracts.Conflict {
			if again, readErr := h.svc.GetRoleByName(ctx, app.GetRoleByNameQuery{
				Caller: caller, Name: preset,
			}); readErr == nil && again.Found {
				return again.Role.ID, nil
			}
		}
		return "", err
	}
	return created.Role.ID, nil
}

// partialAccount reports an account left without authority.
//
// It records what happened and returns a message a person can act on rather
// than the raw failure, because the account *does* exist and the recovery is a
// button on its own panel. An error saying only "conflict" would leave somebody
// wondering whether to try creating them again.
func (h *Handler) partialAccount(ctx context.Context, user domain.User, what string, err error) error {
	telemetry.From(ctx).For("auth").Error("an account was created without authority",
		telemetry.Identifier("username", user.Username),
		telemetry.String("step", what),
		telemetry.Err(err))
	return contracts.WrapError(contracts.CategoryOf(err),
		user.Username+" was created but "+what+
			", so they cannot sign in yet. Open their account and grant it there", err)
}

// validateNewAccount rejects per field (contracts#13).
//
// It repeats the client's own rules deliberately. The client enforces what it
// can before anything is sent, which is what makes a form feel like it works;
// this is the boundary, which is what makes it correct — the command surface is
// reachable without a form at all.
func validateNewAccount(env createAccountEnvelope) error {
	var fields []contracts.FieldRejection
	reject := func(field, message string) {
		fields = append(fields, contracts.FieldRejection{Field: field, Message: message})
	}
	switch {
	case strings.TrimSpace(env.Username) == "":
		reject("username", "Choose a username for them.")
	case strings.ContainsFunc(env.Username, unicode.IsSpace):
		reject("username", "A username cannot contain spaces.")
	}
	switch {
	case env.Password == "":
		reject("password", "Set a password for them.")
	case len([]rune(env.Password)) < 8:
		reject("password", "Use at least 8 characters.")
	}
	if env.Preset == "" {
		// Not a form field: the action carries it. A submission without one is a
		// client out of step with the screen it was served.
		return contracts.NewError(contracts.InvalidArgument, "create account: a preset is required")
	}
	if len(fields) > 0 {
		return contracts.RejectFields("That account could not be created.", fields...)
	}
	return nil
}

// accountEmail supplies the address CreateLocalUser requires.
//
// The command has always required one and Mosaic sends no mail, so a household
// creating an account for a child who has no address had no way through. Rather
// than relax the command — the field is a real one and other deployments may
// come to depend on it — the transport fills a local, non-routable address that
// is obviously synthetic to anybody who reads it.
func accountEmail(env createAccountEnvelope) string {
	if e := strings.TrimSpace(env.Email); e != "" {
		return e
	}
	return strings.TrimSpace(env.Username) + "@mosaic.invalid"
}

// setUserStatus suspends or reactivates an account.
type statusEnvelope struct {
	UserID string `json:"userId"`
	Status string `json:"status"`
}

func (h *Handler) setUserStatus(ctx context.Context, caller v1.Caller, input []byte) error {
	var env statusEnvelope
	if err := json.Unmarshal(nonEmpty(input), &env); err != nil {
		return contracts.NewError(contracts.InvalidArgument, "set user status: input is not valid JSON")
	}
	if env.UserID == "" || env.Status == "" {
		return contracts.NewError(contracts.InvalidArgument, "set user status: a user and a status are required")
	}
	_, err := h.svc.SetUserStatus(ctx, app.SetUserStatusCommand{
		CallerSessionID: domain.SessionID(caller.Session),
		TargetUserID:    domain.UserID(env.UserID),
		Status:          domain.UserStatus(env.Status),
	})
	return err
}

// grantPresetEnvelope names an account and the preset to give it.
type grantPresetEnvelope struct {
	UserID string `json:"userId"`
	Preset string `json:"preset"`
}

// grantPreset adds a preset's worth of authority to an account that already
// exists.
//
// It goes through the same find-or-create the account creation does, so an
// install has one role per preset however it was reached. The permission set is
// the grantor's own narrowed by the preset, and it is only used if the role has
// to be made — granting an existing one is bounded by what the grantor holds.
func (h *Handler) grantPreset(ctx context.Context, caller v1.Caller, input []byte) error {
	var env grantPresetEnvelope
	if err := json.Unmarshal(nonEmpty(input), &env); err != nil {
		return contracts.NewError(contracts.InvalidArgument, "grant preset: input is not valid JSON")
	}
	if env.UserID == "" || env.Preset == "" {
		return contracts.NewError(contracts.InvalidArgument, "grant preset: a user and a preset are required")
	}

	offer, err := h.svc.GrantablePermissions(ctx, app.GrantablePermissionsQuery{
		Caller: caller, Preset: env.Preset,
	})
	if err != nil {
		return err
	}
	if len(offer.Selected) == 0 {
		return contracts.NewError(contracts.PermissionDenied,
			"you hold none of the permissions the "+env.Preset+" preset starts from")
	}
	perms := make([]string, 0, len(offer.Selected))
	for _, p := range offer.Selected {
		perms = append(perms, string(p))
	}

	roleID, err := h.presetRole(ctx, caller, env.Preset, perms)
	if err != nil {
		return err
	}
	_, err = h.svc.GrantRole(ctx, app.GrantRoleCommand{
		CallerSessionID: domain.SessionID(caller.Session),
		UserID:          domain.UserID(env.UserID),
		RoleID:          roleID,
	})
	if err != nil && contracts.CategoryOf(err) == contracts.Conflict {
		// Already granted. Saying so beats "conflict", which reads as a failure
		// for something that is in fact the state the person was asking for.
		return contracts.NewError(contracts.Conflict, "this account already holds the "+env.Preset+" role")
	}
	return err
}

// nonEmpty makes an absent input decode to the zero value rather than failing.
// A form submitted with nothing in it sends nothing, and the validation above is
// what should say so — per field, where somebody can see it.
func nonEmpty(input []byte) []byte {
	if len(input) == 0 {
		return []byte("{}")
	}
	return input
}
