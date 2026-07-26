// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	sdui "github.com/mosaic-media/contracts/sdui"
	"github.com/mosaic-media/contracts/ui"

	"github.com/mosaic-media/platform/internal/platform/app"
)

// The doorway (ADR 0101, carrying ADR 0098's two states).
//
// It is a screen like any other — composed from the vocabulary that already
// exists, emitted by the Platform, drawn by a client that bundles nothing. What
// makes it different is only when it is asked for: before a session, through
// AuthService.Bootstrap, which sends the skin and the definitions it needs
// alongside it because a client at this point has neither.
//
// **The server picks which one.** The client is not told which state it is in;
// it is shown one. That is why there is no screen name on the wire and no
// params — a doorway is not a route, and a client that could ask for the setup
// tree could ask for it on a claimed server.
//
// What is deliberately not here yet is the form. This slice delivers the
// doorway's *vocabulary* — the mechanism that carries a pre-session tree with
// everything needed to draw it — and the sign-in and claim flows are the next
// milestone. A control wired to nothing would be worse than none: an affordance
// with nothing behind it is the dead end this whole thread exists to remove
// (ADR 0036). So each state says what it is and what will happen, and offers
// nothing it cannot do.

// Doorway builds the pre-session tree for the state this server is in.
func Doorway(state app.ServerState) sdui.Node {
	if !state.Claimed {
		return setupDoorway()
	}
	return signInDoorway()
}

// signInDoorway is what a claimed server shows: a locked door that says so.
func signInDoorway() sdui.Node {
	return ui.Screen(
		ui.Title(doorwayTitle),
		ui.Subtitle("Sign in to continue."),
		ui.Section("Sign in",
			ui.Banner(
				"This server has been claimed. Signing in from this screen is the next thing to be built — "+
					"until it is, each client signs in with the credentials it was configured with.",
				ui.ToneInfo),
		),
	).Build()
}

// setupDoorway is what an unclaimed server shows.
//
// It states that nobody owns the server and stops there. It does not name the
// environment variables that seed an administrator, or say how many accounts
// exist, because this screen is reachable by anyone who can reach the port.
func setupDoorway() sdui.Node {
	return ui.Screen(
		ui.Title(doorwayTitle),
		ui.Subtitle("This server has not been set up yet."),
		ui.Section("Set up",
			ui.Banner(
				"Nobody has claimed this server. The first person to arrive will claim it and create the "+
					"accounts that use it — that step is the next thing to be built.",
				ui.ToneInfo),
		),
	).Build()
}

// doorwayTitle is what the door says it is. The product name and nothing about
// the install: a server name is a thing a household chose, and the doorway is
// the one surface reachable before authentication.
const doorwayTitle = "Mosaic"
