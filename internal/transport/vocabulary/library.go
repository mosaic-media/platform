// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package vocabulary

import (
	"github.com/mosaic-media/contracts/definitions"
	"github.com/mosaic-media/contracts/tokens"
)

// The two things a client is handed before it can draw anything (contracts#4):
// the components, and the skin. They live here rather than in either transport
// because both transports serve them — the session pushes them on connect, and
// the pre-session bootstrap (platform#57) sends the doorway's share of them to a
// client that has no session to be pushed to.

// definitionLibrary is the contract's component library.
//
// It comes from the contract, which is where components are authored. Do not
// keep a copy in this repository: a second set of the same names in a client
// drifts from the contract's, silently.
var definitionLibrary = mustDefinitions()

// Library returns the whole component library, as the session pushes it on
// connect. The pre-session path sends a Subset of it instead.
func Library() []byte { return definitionLibrary }

// mustDefinitions loads the contract's component library at start-up. A failure
// here is a broken build artefact rather than a runtime condition — the library
// is embedded in the contract module — and a Platform that served a malformed
// one would leave every client with nothing to render screens with, which is a
// worse failure to diagnose than not starting.
func mustDefinitions() []byte {
	raw, err := definitions.Library()
	if err != nil {
		panic("sdui: the contract's component library is unreadable: " + err.Error())
	}
	return raw
}

// tokenSet is the design token set the Platform serves — the skin half of what a
// client is handed (contracts#4's UI-library tier). Keeping the values here rather
// than in a client's stylesheet is what makes a re-skin something other than a
// client release.
var tokenSet = mustTokens()

// Tokens returns the DTCG token document, applied by a client before it draws
// anything.
func Tokens() []byte { return tokenSet }

func mustTokens() []byte {
	if err := tokens.Validate(); err != nil {
		panic("sdui: the contract's token set is unusable: " + err.Error())
	}
	return tokens.Set()
}
