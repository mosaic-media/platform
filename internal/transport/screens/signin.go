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
	fieldUsername = "username"
	fieldPassword = "password"
)

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
