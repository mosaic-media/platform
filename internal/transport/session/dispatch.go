// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/screens"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
	sessionv1 "github.com/mosaic-media/sdui/gen/mosaic/session/v1"
)

// dispatch routes an Invoke action to the application command service that backs
// it. This is the first-party GraphQL split of ADR 0041: the client session no
// longer runs its mutations through the GraphQL schema (as ADR 0032's socket
// did) — it calls the application services directly, the same services the
// GraphQL resolvers call. GraphQL is retained only as the external/tooling
// surface. The action's caller is the session's opaque ref (ADR 0017), so every
// write re-authorises as the invoking user.
//
// input is the SDUI runtime's action envelope in JSON (ADR 0029). Each case
// decodes the same shape the corresponding GraphQL resolver accepts, so an
// action ABI does not change with the transport.
func (h *Handler) dispatch(ctx context.Context, s *liveSession, action string, input []byte) (*sessionv1.ServerMessage, error) {
	caller := s.caller
	switch action {
	case "importContent":
		ref, err := importRefFromInput(input)
		if err != nil {
			return nil, err
		}
		_, err = h.svc.ImportContent(ctx, app.ImportContentCommand{Caller: caller, Ref: ref})
		return nil, err
	case "configureModule":
		moduleID, settings, err := configureFromInput(input)
		if err != nil {
			return nil, err
		}
		_, err = h.svc.ConfigureModule(ctx, app.ConfigureModuleCommand{Caller: caller, ModuleID: moduleID, Settings: settings})
		return nil, err
	case "playPart":
		return h.playPart(ctx, caller, input)
	default:
		return nil, contracts.NewError(contracts.InvalidArgument, "unknown action: "+action)
	}
}

// playEnvelope is the playPart action input: the Part to play. The SDUI Play
// action emits `partId` (ADR 0029's action ABI), so that is the key read here.
type playEnvelope struct {
	PartID string `json:"partId"`
	Title  string `json:"title"`
	Poster string `json:"poster"`
}

// playPart resolves a Part to playable bytes, seals the result into a ticket and
// returns the Player surface to push (ADR 0045, ADR 0047).
//
// It is the one dispatch case that produces a surface rather than a toast,
// because playback *is* a surface: the client has to be handed somewhere to
// render, and the screen underneath must survive it.
//
// The ticket is minted here, in the transport, and never leaves the server in
// readable form — the resolved URL may carry a debrid credential, so the client
// receives an opaque handle to the Platform's own origin instead.
func (h *Handler) playPart(ctx context.Context, caller v1.Caller, input []byte) (*sessionv1.ServerMessage, error) {
	var env playEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return nil, contracts.NewError(contracts.InvalidArgument, "play part: input is not valid JSON")
		}
	}
	if env.PartID == "" {
		return nil, contracts.NewError(contracts.InvalidArgument, "play part: a part id is required")
	}
	if h.tickets == nil {
		return nil, contracts.NewError(contracts.Unavailable, "playback is not configured on this Platform")
	}

	res, err := h.svc.ResolvePlayback(ctx, app.ResolvePlaybackQuery{Caller: caller, PartID: v1.PartID(env.PartID)})
	if err != nil {
		return nil, err
	}

	// Whether the container needs rewriting is decided here, with the resolved
	// location in hand, and travels sealed inside the ticket — the origin does
	// not re-derive it per range request (ADR 0048).
	remux := playback.ShouldRemux(res.URL)
	ticket, err := h.tickets.Mint(res.URL, res.Headers, caller.Session, remux)
	if err != nil {
		return nil, contracts.WrapError(contracts.Internal, "mint playback ticket", err)
	}

	node := screens.PlayerNode(screens.PlayerParams{
		Src:    "/playback/" + ticket,
		Title:  env.Title,
		Poster: env.Poster,
		// A remuxed stream is fragmented MP4 off a pipe; naming the type lets a
		// client pick its pipeline before it fetches a byte.
		MimeType: playbackMimeType(remux),
	})
	return regionMsg(playerRegion, sessionv1.RegionUpdate_REPLACE, node), nil
}

// playbackMimeType names what the origin will serve. A remuxed stream is always
// fragmented MP4; a relayed one keeps whatever the upstream sends, which the
// client discovers from the response rather than being told up front.
func playbackMimeType(remux bool) string {
	if remux {
		return "video/mp4"
	}
	return ""
}

// importEnvelope is the importContent action input: a content ref to materialise
// (ADR 0028), under the runtime's `ref` key — the same shape the detail screen's
// Add-to-library action emits and the GraphQL importContent resolver reads.
type importEnvelope struct {
	Ref struct {
		Provider       string `json:"provider"`
		NativeID       string `json:"nativeId"`
		NativeType     string `json:"nativeType"`
		MediaType      string `json:"mediaType"`
		ExternalScheme string `json:"externalScheme"`
		ExternalID     string `json:"externalId"`
	} `json:"ref"`
}

// importRefFromInput decodes an importContent action envelope into a ContentRef.
func importRefFromInput(input []byte) (v1.ContentRef, error) {
	var env importEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return v1.ContentRef{}, contracts.NewError(contracts.InvalidArgument, "import content: input is not valid JSON")
		}
	}
	return v1.ContentRef{
		Provider:       env.Ref.Provider,
		NativeID:       env.Ref.NativeID,
		NativeType:     env.Ref.NativeType,
		MediaType:      v1.MediaType(env.Ref.MediaType),
		ExternalScheme: env.Ref.ExternalScheme,
		ExternalID:     env.Ref.ExternalID,
	}, nil
}

// configureEnvelope is the configureModule action input: a module id and its
// opaque settings document (ADR 0021), the shape a module's contributed settings
// UI (ADR 0038) drives and the GraphQL configureModule resolver reads.
type configureEnvelope struct {
	ModuleID string          `json:"moduleId"`
	Settings json.RawMessage `json:"settings"`
}

// configureFromInput decodes a configureModule action envelope. settings arrives
// as a JSON object and is carried through opaquely — the Platform stores it
// without interpreting it (ADR 0021).
func configureFromInput(input []byte) (string, []byte, error) {
	var env configureEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return "", nil, contracts.NewError(contracts.InvalidArgument, "configure module: input is not valid JSON")
		}
	}
	var settings []byte
	if len(env.Settings) > 0 && string(env.Settings) != "null" {
		settings = env.Settings
	}
	return env.ModuleID, settings, nil
}
