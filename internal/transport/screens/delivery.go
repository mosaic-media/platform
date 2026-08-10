// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package screens

import (
	"context"

	"github.com/mosaic-media/platform/internal/transport/playback"
)

// The client's declared decoding ability, carried to the emit-side.
//
// A detail screen says how a release will be delivered — direct play, or which
// stream has to be re-encoded and why (platform#29). That answer is not a property
// of the release alone: the same file direct-plays on one client and is remuxed
// for another, so stating it needs what the *asking* client declared on Attach
// (platform#28). Until now that declaration lived only on the live session and was
// read only at play time, so the plan was computed, used to mint one ticket and
// discarded — and the screen that would have shown it had no way to ask.
//
// It rides the context rather than Render's signature deliberately. Every screen
// implements that signature and exactly one of them wants this; widening it
// would make ten builders carry a parameter to hand to the eleventh. A
// request-scoped value that most handlers ignore is what a context is for.
//
// A context with no profile on it yields the browser assumption, which is what
// the Platform used everywhere before clients declared anything — so a caller
// that does not set it degrades to the previous behaviour rather than to no
// answer.

type clientCodecsKey struct{}

// WithClientCodecs carries what the asking client can decode into a render.
func WithClientCodecs(ctx context.Context, c playback.ClientCodecs) context.Context {
	return context.WithValue(ctx, clientCodecsKey{}, c)
}

// clientCodecs reads the declaration back, falling back to the browser default.
func clientCodecs(ctx context.Context) playback.ClientCodecs {
	if c, ok := ctx.Value(clientCodecsKey{}).(playback.ClientCodecs); ok {
		return c
	}
	return playback.DefaultBrowserCodecs
}
