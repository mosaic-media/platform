// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package postgres_test

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// fakeStreamModule is a capability that fills only RoleStream. It stands in for a
// real stream source (Stremio, AIOStreams) in a Platform test so the test
// exercises the Platform's own behaviour — the enrichment bridge, playback
// resolution — without the platform module importing an extension module.
//
// That import is the coupling the tier split forbids (ADR 0079, ADR 0081):
// extension modules are downloaded and run at runtime, not compiled into the
// Platform, so they are not platform dependencies and must not appear in its
// test graph either. What a *real* extension module does out of process is
// proven by a separate integration surface that installs one at runtime; what
// the *Platform* does when a stream provider answers is proven here, against a
// double that answers deterministically.
//
// streamsFor is the whole of its behaviour: given the request the Platform sends
// it, it returns the streams it offers, or none to decline. A test sets it to
// assert the Platform passed the identity, season and episode it expects — which
// is what keeps the double from turning a bridge assertion into a tautology.
type fakeStreamModule struct {
	id         string
	streamsFor func(v1.StreamRequest) []v1.StreamLink
}

func (m *fakeStreamModule) Manifest() v1.Manifest {
	return v1.Manifest{ID: m.id, Name: m.id, Provides: []v1.Role{v1.RoleStream}}
}

func (m *fakeStreamModule) Import(context.Context, v1.ContentService, v1.ImportRequest) (v1.ImportResult, error) {
	// A stream source fills no read role, so it never materialises content on its
	// own — it is only ever reached through the enrichment fan-out (ADR 0073).
	return v1.ImportResult{}, nil
}

func (m *fakeStreamModule) Streams(_ context.Context, req v1.StreamRequest) (v1.StreamResponse, error) {
	if m.streamsFor == nil {
		return v1.StreamResponse{}, nil
	}
	return v1.StreamResponse{Streams: m.streamsFor(req)}, nil
}

var (
	_ v1.Capability     = (*fakeStreamModule)(nil)
	_ v1.StreamProvider = (*fakeStreamModule)(nil)
)
