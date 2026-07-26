// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package auth

import (
	"context"
	"errors"

	"connectrpc.com/connect"

	authv1 "github.com/mosaic-media/contracts/gen/mosaic/auth/v1"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/screens"
	"github.com/mosaic-media/platform/internal/transport/vocabulary"
)

// Bootstrap is the pre-session call (ADR 0101): the skin, the definitions the
// doorway needs, and the doorway, in one response.
//
// It exists because a client without a session has no vocabulary *at all* —
// definitions and the token set are pushed on connect, and the client
// deliberately bundles none (ADR 0082). The sign-in screen that was built and
// withdrawn on the same day is what that costs: the Platform served exactly the
// right tree and the browser drew "SignInPanel — not registered in this Shell",
// unstyled, because neither half had arrived.
//
// Three properties are load-bearing and each is easy to lose later:
//
//   - **The definitions are a subset, transitively closed over the tree.** This
//     is the one payload an unauthenticated party can enumerate, so it describes
//     a doorway and nothing else. The subset is the security property, not an
//     optimisation.
//   - **Negotiation applies unchanged** (ADR 0084). The request carries the same
//     VocabularyProfile Attach carries, so the doorway is degraded and its
//     definitions fallback-selected exactly as every screen after it is.
//   - **It does not vary on identity.** Nothing here reads a username, so
//     nothing here can be used to learn which usernames exist.
func (h *Handler) Bootstrap(ctx context.Context, req *connect.Request[authv1.BootstrapRequest]) (*connect.Response[authv1.BootstrapResponse], error) {
	// Rate-limited because it is the one surface reachable before
	// authentication (ADR 0101). It is cheap — one store read and some embedded
	// bytes — so the limit is against volume rather than cost.
	if !h.bootstrapLimit.allow(peerOf(req), h.now()) {
		return nil, connect.NewError(connect.CodeResourceExhausted,
			errors.New("too many bootstrap requests; try again shortly"))
	}

	client := vocabulary.From(req.Msg.GetVocabulary())
	state := h.svc.ServerState(ctx)

	// The server picks the tree, unchanged from ADR 0098. The client is not told
	// which it got: there is no screen name on the wire, because a client that
	// could ask for the setup tree could ask for it on a claimed server.
	tree := screens.Doorway(state)
	degraded, dropped := vocabulary.Degrade(tree, client)
	dropped.Report(ctx, "", "bootstrap")

	subset, err := vocabulary.Subset(vocabulary.Library(), degraded)
	if err != nil {
		// The library is an embedded build artefact, so this is a broken build
		// rather than a runtime condition. Serving the whole library instead
		// would be the quiet failure this call exists to prevent — a doorway
		// that discloses every component in the design system — so it fails.
		return nil, connect.NewError(connect.CodeInternal,
			errors.New("the component library could not be subset for the doorway"))
	}
	// Fallback selection runs over the subset rather than the library, and after
	// it: a fallback template may name components of its own, which Subset
	// already followed, so filtering second cannot leave a dangling reference.
	subset = vocabulary.DefinitionsFor(ctx, client, subset, "")

	primitives, actions := client.Counts()
	telemetry.From(ctx).For("auth").Info("pre-session bootstrap served",
		telemetry.Bool("claimed", state.Claimed),
		telemetry.Bool("vocabulary_declared", client.Declared()),
		telemetry.String("vocabulary_version", client.Version()),
		telemetry.Int("client_primitives", primitives),
		telemetry.Int("client_actions", actions),
		telemetry.Int("definitions", countDefinitions(subset)))

	return connect.NewResponse(&authv1.BootstrapResponse{
		Tokens:      vocabulary.Tokens(),
		Definitions: subset,
		UiNode:      degraded,
	}), nil
}
