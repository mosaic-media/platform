// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import "github.com/mosaic-media/platform/internal/platform/policy"

// Permission presets (platform#44).
//
// These are starting points, not tiers. Authority in Mosaic is granular: what an
// account may do is the set of actions granted to it, and nothing reads a role's
// name to decide anything. A preset fills a grant form in, and the grantor then
// removes whatever they do not want to hand over.
//
// The boundary that holds is enforced elsewhere and does not mention these at
// all: nobody may grant authority they do not themselves hold (delegation.go).
// An administrator whose own set was trimmed granting PresetAdministrator
// confers the intersection, with no special case here.
//
// A preset is a convenience for whoever is granting. Do not read one as a
// security concept: that is how a system with granular permissions grows a
// shadow tier model disagreeing with its own checks.
const (
	// PresetNameSuperuser is the account created on first boot: the person who
	// owns the server and performed setup. It holds everything, because it is
	// the root of every other grant — an authority withheld from it could
	// never be given to anyone.
	PresetNameSuperuser = "Superuser"
	// PresetNameAdministrator runs the install: content, modules, users,
	// configuration.
	PresetNameAdministrator = "Administrator"
	// PresetNameUser watches things. It is the preset for an ordinary account
	// on a household install.
	PresetNameUser = "User"
)

// userActions is what an ordinary account needs: reach the library, play from
// it, and keep its own settings.
func userActions() []policy.Action {
	return []policy.Action{
		// Signing in, and it must stay in this preset: an account without
		// session.create is refused a session with the right password and an
		// active status, and the only sign is a policy denial. It is the floor
		// of having an account at all.
		ActionSessionCreate,
		ActionContentRead,
		ActionContentResolve,
		// Where they got to (platform#26). These belong to the ordinary preset
		// rather than the administrator one, which is why they are separate
		// actions from content.read and content.create: a household member who
		// may watch everything and change nothing is the normal arrangement.
		ActionPlaybackWrite,
		ActionPlaybackRead,
		ActionPreferenceWrite,
		ActionPreferenceRead,
	}
}

// administratorActions adds running the install to that: curating the graph,
// configuring the modules that feed it, managing accounts, and configuration.
func administratorActions() []policy.Action {
	return append(userActions(),
		ActionUserCreate, ActionUserRead, ActionUserList, ActionUserStatusUpdate,
		// ActionSessionRevoke is somebody else's session. Ending your own needs
		// no permission at all — see RevokeSession, where owning the target
		// stands in for the grant.
		ActionSessionRevoke,
		ActionPermissionRead,
		ActionRoleCreate, ActionRoleGrant,
		ActionConfigDraft, ActionConfigValidate, ActionConfigActivate, ActionConfigRead,
		ActionContentCreate, ActionContentRelate, ActionContentBind, ActionContentImport,
		ActionModuleConfigure, ActionModuleRead, ActionExtensionManage,
		// What the library should contain (platform#60). Administrator rather
		// than superuser: a rule is curation. Reading is a separate action from
		// managing because they are different disclosures, even though this
		// preset confers both.
		ActionLibraryRuleRead, ActionLibraryRuleManage,
		// The resolution register (platform#74). Administrator, because a
		// finding is about the install rather than about the person reading, and
		// acting on it changes what this install is for everybody.
		ActionFindingsRead, ActionFindingsResolve,
	)
}

// superuserActions adds insight: what everyone did.
//
// telemetry.read and its neighbours reveal which screens each user opened and
// what they searched for. Values are redacted at construction (platform#34), but
// the shape of a person's activity survives redaction, so running the install
// does not imply it. A superuser can still grant it to an administrator
// individually.
func superuserActions() []policy.Action {
	return append(administratorActions(),
		ActionTelemetryRead, ActionTelemetryExport, ActionTelemetryConfigure,
		// Reading the background-work queue is insight about the install rather
		// than about a person, so it lands in the same tier: a superuser sees
		// the queue, and an administrator is granted it deliberately.
		// telemetry.configure, already here, is what the retention sweep
		// authorises — so the sweep running as the system principal and an
		// administrator running one by hand need the same permission.
		ActionJobRead,
		// The credential tables' own housekeeping (platform#58). Install-level
		// rather than about any one person's session, which is why it is here
		// and not in the administrator preset beside user.session.revoke.
		ActionSessionMaintain,
		// ActionAuditRead and ActionAuditExport join here when the audit store
		// is built (platform#35) — same category.
	)
}

// Preset returns the actions a named preset starts from, and whether the name
// is one this Platform offers.
//
// A grantor is never limited to these: the set they may actually confer is
// bounded by their own permissions, which the delegation check applies to
// whatever they submit.
func Preset(name string) ([]policy.Action, bool) {
	switch name {
	case PresetNameSuperuser:
		return superuserActions(), true
	case PresetNameAdministrator:
		return administratorActions(), true
	case PresetNameUser:
		return userActions(), true
	default:
		return nil, false
	}
}

// SuperuserActions is the full action set, used to seed the first account.
func SuperuserActions() []policy.Action { return superuserActions() }

// AdministratorActions is the administrator preset.
func AdministratorActions() []policy.Action { return administratorActions() }

// UserActions is the ordinary-account preset.
func UserActions() []policy.Action { return userActions() }
