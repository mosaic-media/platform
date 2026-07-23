// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import "github.com/mosaic-media/platform/internal/platform/policy"

// Permission presets (ADR 0069).
//
// **These are starting points, not tiers.** Authority in Mosaic is granular:
// what an account may do is the set of actions granted to it, and nothing
// reads a role's *name* to decide anything. A preset is what a grantor starts
// from before ticking boxes off — "Administrator" fills the form in, and the
// grantor then removes whatever they do not want to hand over.
//
// The boundary that actually holds is enforced elsewhere and does not mention
// these at all: nobody may grant authority they do not themselves hold
// (delegation.go). That rule is what makes handing out a preset safe, because
// an account whose own set was trimmed can only ever pass on what survived the
// trimming — a "PresetAdministrator" granted by a reduced administrator is
// silently reduced to the intersection, which is the correct answer and needs
// no special case.
//
// So a preset is a convenience for whoever is granting. It is deliberately not
// a security concept, and reading one as though it were is how a system with
// granular permissions grows a shadow tier model that disagrees with its own
// checks.
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
		ActionContentRead,
		ActionContentResolve,
		ActionPreferenceWrite,
		ActionPreferenceRead,
	}
}

// administratorActions adds running the install to that: curating the graph,
// configuring the modules that feed it, managing accounts, and configuration.
func administratorActions() []policy.Action {
	return append(userActions(),
		ActionUserCreate, ActionUserRead, ActionUserList, ActionUserStatusUpdate,
		ActionSessionCreate, ActionSessionRevoke,
		ActionPermissionRead,
		ActionRoleCreate, ActionRoleGrant,
		ActionConfigDraft, ActionConfigValidate, ActionConfigActivate, ActionConfigRead,
		ActionContentCreate, ActionContentRelate, ActionContentBind, ActionContentImport,
		ActionModuleConfigure, ActionModuleRead,
	)
}

// superuserActions adds insight: what everyone did.
//
// telemetry.read and its neighbours reveal which screens each user opened and
// what they searched for. Values are redacted at construction (ADR 0056), but
// the shape of a person's activity survives redaction, so it is not something
// running the install implies. A superuser can still grant it to an
// administrator individually — that is the whole point of it being an action
// rather than a tier.
func superuserActions() []policy.Action {
	return append(administratorActions(),
		ActionTelemetryRead, ActionTelemetryExport, ActionTelemetryConfigure,
		// ActionAuditRead and ActionAuditExport join here when the audit store
		// is built (ADR 0057) — same category, named now so the decision does
		// not need remaking.
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
