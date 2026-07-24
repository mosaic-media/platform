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

// fakeImportModule is a capability that materialises a fixed one-season series
// into the content graph when the Platform invokes it through ImportContent. It
// stands in for a real import source (Stremio) so a Platform test exercises the
// composition-and-invocation path — registry -> ImportContent -> capability ->
// ContentService, each write re-authorising as the caller (ADR 0017) — without
// the platform module importing an extension module. What a real module does
// with an addon's JSON is the module's own test; what the Platform does when a
// capability writes back through the boundary is this one's.
//
// It declares no roles: ImportContent resolves it by id, not by role, and
// declaring one would only oblige it to implement that provider interface.
type fakeImportModule struct {
	id       string
	episodes []fakeImportEpisode
}

type fakeImportEpisode struct {
	title string
	// partRef is a remote reference; empty attaches no part, so a metadata-only
	// import (a tree with no playable Parts) can be modelled too.
	partRef string
}

func (m *fakeImportModule) Manifest() v1.Manifest {
	return v1.Manifest{ID: m.id, Name: m.id}
}

func (m *fakeImportModule) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	// Idempotent like a real source: an identity already in the library is
	// returned, not duplicated — the AlreadyKnown path the second import asserts.
	found, err := svc.FindContentByExternalID(ctx, v1.FindContentByExternalIDQuery{
		Caller: req.Caller, Scheme: req.Ref.ExternalScheme, Value: req.Ref.ExternalID,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	for _, node := range found.Nodes {
		if node.IsRoot() {
			return v1.ImportResult{WorkID: node.ID, AlreadyKnown: true}, nil
		}
	}

	work, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller: req.Caller, MediaType: req.Ref.MediaType, Title: m.id + " series",
		ExternalIDs: []byte(`{"` + req.Ref.ExternalScheme + `":"` + req.Ref.ExternalID + `"}`),
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	res := v1.ImportResult{WorkID: work.Work.ID}

	if _, err := svc.BindContentSource(ctx, v1.BindContentSourceCommand{
		Caller: req.Caller, NodeID: work.Work.ID,
		SourceProvider: m.id, SourceRef: req.Ref.ExternalID,
		MatchConfidence: 1, MatchMethod: v1.MatchExternalIDExact, Status: v1.BindingConfirmed,
	}); err != nil {
		return v1.ImportResult{}, err
	}

	season, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
		Caller: req.Caller, ParentID: work.Work.ID,
		Kind: v1.NodeContainer, ContainerType: v1.ContainerSeason,
		Title: "Season 1", NaturalOrder: 1,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	res.Containers++

	for i, ep := range m.episodes {
		item, err := svc.AddContentChild(ctx, v1.AddContentChildCommand{
			Caller: req.Caller, ParentID: season.Node.ID,
			Kind: v1.NodeItem, ItemType: v1.ItemEpisode,
			Title: ep.title, NaturalOrder: float64(i + 1),
		})
		if err != nil {
			return v1.ImportResult{}, err
		}
		res.Items++
		if ep.partRef != "" {
			if _, err := svc.AttachContentPart(ctx, v1.AttachContentPartCommand{
				Caller: req.Caller, NodeID: item.Node.ID, Role: v1.PartEdition,
				Location: v1.MediaLocation{Scheme: v1.RemoteLocation, Provider: m.id, Ref: ep.partRef},
			}); err != nil {
				return v1.ImportResult{}, err
			}
			res.Parts++
		}
	}
	return res, nil
}

var _ v1.Capability = (*fakeImportModule)(nil)
