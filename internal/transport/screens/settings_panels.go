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

// The Platform's own settings panels.
//
// The design draws thirteen of these. Two are here, and the gap is capability
// rather than layout: Playback and Appearance would be switches writing
// preferences nothing reads, and Sources, Storage, Transcoding, Server and
// Metrics have no backing at all — no filesystem scanner, no disk accounting, no
// transcoding configuration, no jobs runner, no metrics store. A nav row leading
// to a panel that cannot answer anything is the dead end ADR 0036 exists to
// remove, so those rows are not drawn either.

// accountPanel says who this viewer is signed in as.
//
// It is read-only, and that is a statement about the Platform rather than a
// design choice: there is no command to change a display name, a username or an
// email. `CreateLocalUser` makes one and `SetUserStatus` enables or disables it;
// nothing updates a profile. So the design's four inputs and its Save button are
// four facts here — drawing editable fields over a mutation that does not exist
// would be worse than saying plainly what Mosaic knows.
//
// Absent for the same reason: the avatar (no field on domain.User and nowhere to
// put an image), "Change password" (the credential store can save one, but no
// application command exposes it), two-factor (the factors are password, passkey
// and recovery — there is no TOTP), and "Sign out everywhere" with its device
// count (SessionStore revokes one session by id and cannot list a user's).
func (s *Service) accountPanel(ctx context.Context, caller v1.Caller, nav settingsNavModel) (sdui.Node, error) {
	res, err := s.content.GetCurrentUser(ctx, app.GetCurrentUserQuery{Caller: caller})
	if err != nil {
		return nil, err
	}
	u := res.User

	rows := []ui.El{}
	row := func(label, value string) {
		if value != "" {
			rows = append(rows, ui.SettingsRow(label, ui.Value(value)))
		}
	}
	row("Display name", u.DisplayName)
	row("Username", u.Username)
	row("Email", u.Email)
	row("Status", titleWords(string(u.Status)))
	if !u.CreatedAt.IsZero() {
		row("Member since", u.CreatedAt.Format("2 January 2006"))
	}

	return settingsFrame(nav, sectionAccount, "Account", "Your profile on this server.",
		ui.Stack("vertical", 0, rows...).Build(),
	), nil
}

// peoplePanel lists who can reach this server and what they are.
//
// The design's "Invite" control and its per-person "last seen" are absent: there
// is no invitation flow and no session listing to read a last-seen from. What is
// here is real — the accounts, their roles and whether each is active — and each
// row's roles are the grants that actually decide what that person may do.
func (s *Service) peoplePanel(ctx context.Context, caller v1.Caller, nav settingsNavModel) (sdui.Node, error) {
	res, err := s.content.ListUsers(ctx, app.ListUsersQuery{
		CallerSessionID: domain.SessionID(caller.Session),
	})
	if err != nil {
		return nil, err
	}
	if len(res.Users) == 0 {
		return settingsFrame(nav, sectionPeople, "People",
			"Who can reach this server, and what they are allowed to do.",
			ui.EmptyState(emptyIconCollections, "No accounts on this server").Build(),
		), nil
	}

	rows := make([]ui.El, 0, len(res.Users))
	for _, u := range res.Users {
		name := u.DisplayName
		if name == "" {
			name = u.Username
		}
		summary := u.Username
		if roles := s.rolesFor(ctx, caller, u.ID); roles != "" {
			summary = roles + " · " + u.Username
		}
		rows = append(rows, ui.SettingsRow(name,
			ui.Summary(summary),
			ui.Value(titleWords(string(u.Status)))))
	}

	return settingsFrame(nav, sectionPeople, "People",
		fmt.Sprintf("%d %s on this server, and what each is allowed to do.",
			len(res.Users), plural(len(res.Users), "account")),
		ui.Stack("vertical", 0, rows...).Build(),
	), nil
}

// rolesFor names a person's roles for their row, empty when they hold none or
// the read fails.
//
// A failure costs the roles and never the row: a People panel that will not
// render because one role lookup timed out is worse than one that lists the
// accounts without saying what they are.
func (s *Service) rolesFor(ctx context.Context, caller v1.Caller, userID domain.UserID) string {
	res, err := s.content.GetRolesForUser(ctx, app.GetRolesForUserQuery{
		CallerSessionID: domain.SessionID(caller.Session),
		TargetUserID:    userID,
	})
	if err != nil || len(res.Roles) == 0 {
		return ""
	}
	names := make([]string, 0, len(res.Roles))
	for _, r := range res.Roles {
		names = append(names, r.Name)
	}
	return strings.Join(names, ", ")
}
