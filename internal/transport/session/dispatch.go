// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/screens"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
	sessionv1 "github.com/mosaic-media/sdui/gen/mosaic/session/v1"
)

// dispatch routes an Invoke action to the application command service that backs
// it. ADR 0041 moved the client's mutations off the GraphQL schema ADR 0032's
// socket ran them through and onto the application services directly; ADR 0061
// then removed that schema, so this switch is now the *only* way a client
// mutation reaches the Platform. The action's caller is the session's opaque ref
// (ADR 0017), so every write re-authorises as the invoking user.
//
// That makes the switch a real boundary rather than a convenience: an action a
// client can name and this function cannot map does not exist. Adding one is a
// deliberate act here, which is the point — the surface a client can reach is
// enumerated in one readable place instead of inferred from a schema.
//
// input is the SDUI runtime's action envelope in JSON (ADR 0029), so an action
// ABI is a property of the action, not of the transport carrying it.
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

	res, err := h.svc.ResolvePlayback(ctx, app.ResolvePlaybackQuery{
		Caller: caller, PartID: v1.PartID(env.PartID),
		// The browser's decoding limits, stated where selection can use them.
		// It stands in until clients declare a profile on Attach (ADR 0047) —
		// hard-coding it here is honest for one client and would be a lie for
		// four, which is why the declaration is the real answer.
		Prefer: browserPreference(),
	})
	if err != nil {
		return nil, err
	}

	// Probe the winner, then decide per stream (ADR 0050). This is where a 4K
	// HEVC release with four E-AC3 audio tracks becomes "copy the video, encode
	// the English audio" rather than either a whole-file transcode or a silent
	// film. The plan travels sealed inside the ticket, so the origin does not
	// re-probe on every range request a seeking player makes.
	plan := h.planFor(ctx, res.URL, res.Headers)

	// One record saying what was chosen and what will happen to it. Playback has
	// three independent places to go wrong — selection, probing, and the encode
	// plan — and from the outside every one of them looks like "it still
	// buffers". This makes which of them fired answerable from the log.
	//
	// The release name is the one field here that is not structural: it is
	// free text from a third-party source and it names what someone is
	// watching, so it is classified rather than written verbatim.
	telemetry.From(ctx).For("playback").Info("stream chosen",
		telemetry.Sensitive("release", res.Release),
		telemetry.Int("candidates", res.Candidates),
		telemetry.String("video_codec", res.VideoCodec),
		telemetry.String("audio_codec", res.AudioCodec),
		telemetry.Int("height", res.Height),
		telemetry.Bool("direct_play", plan.DirectPlay),
		telemetry.String("reason", plan.Reason))
	ticket, err := h.tickets.Mint(res.URL, res.Headers, caller.Session, plan)
	if err != nil {
		return nil, contracts.WrapError(contracts.Internal, "mint playback ticket", err)
	}

	node := screens.PlayerNode(screens.PlayerParams{
		Src:    "/playback/" + ticket,
		Title:  env.Title,
		Poster: env.Poster,
		// Anything ffmpeg produces is fragmented MP4; naming the type lets a
		// client pick its pipeline before it fetches a byte. A relayed stream
		// keeps whatever the upstream sends, which the client discovers from
		// the response rather than being told up front.
		MimeType: playbackMimeType(plan),
	})
	return regionMsg(playerRegion, sessionv1.RegionUpdate_REPLACE, node), nil
}

// planFor probes the resolved location and decides how to carry each stream
// (ADR 0050).
//
// A probe failure is not a playback failure: relaying unprobed is exactly what
// happened before probing existed, so an absent ffprobe — or a source that will
// not answer one — degrades to the previous behaviour rather than refusing to
// play. The cost of that fallback is a silent film when the audio turns out to
// be undecodable, which is the bug this exists to fix; it is still better than
// no picture at all.
func (h *Handler) planFor(ctx context.Context, url string, headers map[string]string) playback.Plan {
	if h.prober == nil || !h.prober.Available() {
		return playback.Plan{DirectPlay: true}
	}
	info, err := h.prober.Probe(ctx, url, headers)
	if err != nil {
		return playback.Plan{DirectPlay: true}
	}
	return playback.Decide(info, playback.DefaultBrowserCodecs, nil)
}

// playbackMimeType names what the origin will serve.
func playbackMimeType(plan playback.Plan) string {
	if !plan.DirectPlay {
		return "video/mp4"
	}
	return ""
}

// importEnvelope is the importContent action input: a content ref to materialise
// (ADR 0028), under the runtime's `ref` key — the same shape the detail screen's
// Add-to-library action emits.
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
// UI (ADR 0038) drives.
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

// browserPreference is what a desktop browser can play, in the shape selection
// ranks on.
//
// It mirrors playback.DefaultBrowserCodecs and exists for the same reason: HEVC
// is included because a live test proved Chrome decodes it, and AC3/E-AC3/DTS/
// TrueHD are excluded because Chrome decodes none of them in any container —
// which is the single fact that decides whether most real releases have sound.
// Container is left unconstrained: a plain <video src> uses the browser's own
// demuxer, which handles Matroska, so ranking against containers here would
// reject perfectly playable releases.
func browserPreference() app.PlaybackPreference {
	return app.PlaybackPreference{
		VideoCodecs: map[string]bool{"h264": true, "hevc": true, "vp9": true, "av1": true, "vp8": true},
		AudioCodecs: map[string]bool{"aac": true, "mp3": true, "opus": true, "vorbis": true, "flac": true},
	}
}
