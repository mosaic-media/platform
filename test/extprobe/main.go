// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

// Command extprobe is a trivial out-of-process module, and the first thing to
// cross the extension boundary for real (ADR 0064's build order, step 1: "a
// trivial in-repo module implementing one role — nothing user-visible;
// establishes the wire, the handshake and the handle").
//
// It is deliberately not a useful module. What it proves is that the mechanism
// works end to end where the in-package tests could not reach:
//
//   - go-plugin's handshake over a real Unix socket, in a real child process
//   - a manifest read back across the boundary
//   - Import calling *back* into the Platform's ContentService, within the
//     invocation, carrying the Caller handle it was given
//   - one provider role, so role dispatch is exercised, and the seven it does
//     not fill, so the refusal path is too
//
// Its whole main is the line every module author writes. If that stops being
// true, this file is where it shows.
package main

import (
	"context"

	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
	"github.com/mosaic-media/sdk/host"
)

// probe fills RoleSearch and nothing else. The registry resolves roles from the
// manifest rather than by type assertion — a proxy satisfies every provider
// interface — so declaring one role and implementing one role is what makes
// this a usable test of that.
type probe struct{}

func (probe) Manifest() v1.Manifest {
	return v1.Manifest{
		ID:       "extprobe",
		Version:  "v0.1.0",
		Name:     "Extension Probe",
		Provides: []v1.Role{v1.RoleSearch},
	}
}

// Import writes one Work through the ContentService it reaches over the
// callback stream, acting as the Caller it was handed (ADR 0017). The write is
// the point: it is the only way to prove the callback direction works, and the
// counts it returns are what the test asserts against.
func (probe) Import(ctx context.Context, svc v1.ContentService, req v1.ImportRequest) (v1.ImportResult, error) {
	// Telemetry is reached ambiently off the context, exactly as in process
	// (ADR 0059) — a module never holds one.
	v1.TelemetryFrom(ctx).Info("extprobe import",
		v1.String("native_id", req.Ref.NativeID),
		v1.Int("settings_bytes", len(req.Settings)),
	)

	out, err := svc.AddContentWork(ctx, v1.AddContentWorkCommand{
		Caller:    req.Caller,
		MediaType: req.Ref.MediaType,
		Title:     "probe: " + req.Ref.NativeID,
	})
	if err != nil {
		return v1.ImportResult{}, err
	}
	return v1.ImportResult{WorkID: out.Work.ID, Items: 1}, nil
}

// Search echoes its query back as one result. It exists so role dispatch is
// exercised in both directions — the request converted on the way out, the
// result on the way back.
func (probe) Search(_ context.Context, req v1.SearchRequest) (v1.SearchResponse, error) {
	return v1.SearchResponse{Results: []v1.SearchResult{{
		Ref: v1.ContentRef{
			Provider:       "extprobe",
			NativeID:       req.Text,
			NativeType:     "movie",
			MediaType:      v1.MediaMovie,
			ExternalScheme: "probe",
			ExternalID:     req.Text,
		},
		Title: "probe result: " + req.Text,
		Year:  2026,
	}}}, nil
}

func main() {
	host.Serve(probe{})
}
