// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"
)

// signInAction is the one mutation a pre-session tree may carry (ADR 0097).
// There is no session to dispatch it on, so the client interprets it by calling
// AuthService.SignIn with the form scope's values.
const signInAction = "signIn"

// The field names the sign-in form's scope declares. They are the names
// AuthService.SignInRequest uses, because the client merges the scope straight
// into that request and a second spelling would be a mapping nobody can see.
const (
	fieldUsername    = "username"
	fieldPassword    = "password"
	fieldDisplayName = "displayName"
	fieldEmail       = "email"
)

// claimServerAction is the setup tree's counterpart to signIn (ADR 0098) — the
// other thing a client with no session may ask a Platform to do.
const claimServerAction = "claimServer"

// SignInScreen is the tree a client renders before it has a session (ADR 0097).
//
// It is emitted by the Platform like every other screen — the Shell's own UI is
// the states where the Platform cannot answer, and a Platform that is refusing
// to authenticate you is answering.
//
// **It says nothing about the install.** The design draws the household's
// profiles as named avatars, the library's size and the server's name; none is
// here. Each is a fact about the house, on a screen anyone who can reach the
// port can see, and a decision to disclose them belongs in a record and a
// setting rather than in a layout. ADR 0097 carries the reasoning.
//
// It is exported because it is served by the auth transport rather than by
// Render: it has no caller to authorise and no route to resolve.
func (s *Service) SignInScreen(errMessage string) sdui.Node {
	return ui.Component("SignInPanel",
		ui.Title("Welcome back"),
		ui.Lead("Everything you watch, in one place."),
		ui.Brand("Mosaic"),
		ui.When(errMessage != "", ui.Error(errMessage)),
		ui.FormSlot(
			ui.Form(
				ui.Vars([]any{
					map[string]any{"name": fieldUsername, "type": "string"},
					map[string]any{"name": fieldPassword, "type": "string"},
				}),
				ui.SubmitLabel("Sign in"),
				ui.SubmitAction(ui.Invoke(signInAction, nil)),
				ui.When(errMessage != "", ui.Error(errMessage)),
				ui.TextField("Username or email",
					ui.Name(fieldUsername),
					ui.Placeholder("you@example.com"),
					ui.InputType("username")),
				ui.TextField("Password",
					ui.Name(fieldPassword),
					ui.InputType("password")),
			),
		),
	).Build()
}

// SetupScreen is the tree a client renders on a server nobody owns yet
// (ADR 0098). It is served by the same endpoint as the sign-in tree, because a
// doorway has two states and this is which one you are looking at.
//
// **It is one step, and the design draws six.** Owner creation is what can be
// built; naming the server, choosing folders, connecting services and setting
// playback defaults each need a capability that does not exist — there is no
// server-name field, no filesystem scanner, no service connections, no jobs
// runner, and nothing that reads a playback preference. The design's own rail
// says the rest can be changed later in Settings, which is where the ones that
// do exist already live.
//
// The rail therefore lists the step that exists rather than six of which four
// would do nothing, which is the same rule the settings nav follows.
func (s *Service) SetupScreen(errMessage string) sdui.Node {
	return ui.Component("SetupFrame",
		ui.Title("Create the owner account"),
		ui.Lead("This account has full server control. Everyone else is added afterwards, from Settings."),
		ui.Brand("Mosaic"),
		ui.Kicker("First boot · 01"),
		ui.Step("Step 1 of 1"),
		ui.Footnote("Everything else can be set up later in Settings."),
		ui.Slot("nav",
			ui.SettingsNavItem("Administrator", "info",
				ui.Active(true), ui.Summary("Your owner account")),
		),
		ui.FormSlot(
			ui.Form(
				ui.Vars([]any{
					map[string]any{"name": fieldDisplayName, "type": "string"},
					map[string]any{"name": fieldUsername, "type": "string"},
					map[string]any{"name": fieldEmail, "type": "string"},
					map[string]any{"name": fieldPassword, "type": "string"},
				}),
				ui.SubmitLabel("Continue"),
				ui.SubmitAction(ui.Invoke(claimServerAction, nil)),
				ui.When(errMessage != "", ui.Error(errMessage)),
				ui.TextField("Display name",
					ui.Name(fieldDisplayName),
					ui.Placeholder("Alex Rivera"),
					ui.Help("Optional — what other people see.")),
				ui.TextField("Username",
					ui.Name(fieldUsername),
					ui.InputType("username")),
				ui.TextField("Email",
					ui.Name(fieldEmail),
					ui.InputType("email"),
					ui.Help("Optional, and unused for now — password recovery is not built.")),
				ui.TextField("Password",
					ui.Name(fieldPassword),
					ui.InputType("password")),
			),
		),
	).Build()
}
