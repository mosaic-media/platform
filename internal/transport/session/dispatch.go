// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package session

import (
	"context"
	"encoding/json"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	"github.com/mosaic-media/platform/internal/transport/playback"
	"github.com/mosaic-media/platform/internal/transport/screens"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
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
// An action pushes a *sequence* of updates, not one: the two-lane transport
// (ADR 0041) exists so the server drives the client's regions unprompted, and a
// single action can legitimately push more than one region update — a player and
// the "Next episode" control beside it. Most actions push nothing (a nil slice)
// and re-render the content region instead; playPart is the one that pushes.
func (h *Handler) dispatch(ctx context.Context, s *liveSession, action string, input []byte) ([]*sessionv1.ServerMessage, error) {
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
	case "installExtension":
		repository, moduleID, err := extensionRefFromInput(input)
		if err != nil {
			return nil, err
		}
		_, err = h.svc.InstallExtension(ctx, app.InstallExtensionCommand{
			Caller: caller, Repository: repository, ModuleID: moduleID,
		})
		return nil, err
	case "uninstallExtension":
		moduleID, err := extensionModuleIDFromInput(input)
		if err != nil {
			return nil, err
		}
		return nil, h.svc.UninstallExtension(ctx, app.UninstallExtensionCommand{
			Caller: caller, ModuleID: moduleID,
		})
	case "setPreference":
		key, value, err := preferenceFromInput(input)
		if err != nil {
			return nil, err
		}
		_, err = h.svc.SetUserPreference(ctx, app.SetUserPreferenceCommand{
			Caller: caller, Key: key, Value: value,
		})
		return nil, err
	case "playPart":
		return h.playPart(ctx, s, input)
	case "reportProgress":
		// The one action a client sends unprompted and repeatedly. It produces
		// no surface and no toast: a confirmation for something a player emits
		// every few seconds would be noise over the film it is reporting on.
		return nil, h.reportProgress(ctx, s, input)
	case "setWatched":
		cmd, err := setWatchedFromInput(input)
		if err != nil {
			return nil, err
		}
		cmd.Caller = caller
		_, err = h.svc.SetPlaybackFinished(ctx, cmd)
		return nil, err
	default:
		return nil, contracts.NewError(contracts.InvalidArgument, "unknown action: "+action)
	}
}

// playEnvelope is the playPart action input: the Part to play. The SDUI Play
// action emits `partId` (ADR 0029's action ABI), so that is the key read here.
type playEnvelope struct {
	PartID string `json:"partId"`
	// NodeID is the item being played, and the key playback state is stored
	// under (ADR 0046). It is what the client reports its position against, so
	// a play without it works and simply remembers nothing.
	NodeID string `json:"nodeId"`
	Title  string `json:"title"`
	Poster string `json:"poster"`
	// Restart asks for the beginning rather than the stored position — the
	// "Start over" affordance beside Resume. It is a property of *this* play
	// rather than a change to the state, so choosing it does not throw away
	// where the viewer had got to until they watch past it.
	Restart bool `json:"restart"`
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
func (h *Handler) playPart(ctx context.Context, s *liveSession, input []byte) ([]*sessionv1.ServerMessage, error) {
	caller := s.caller
	// What this client said it can decode, declared on Attach (ADR 0047). It is
	// read once and used twice — to rank candidates and to plan the streams —
	// because those two must agree: choosing a release for its codecs and then
	// planning against a different profile would re-encode the thing selection
	// picked precisely to avoid re-encoding.
	profile := s.clientProfile()

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
		Prefer: profile.prefer, CapabilityClass: profile.class,
	})
	if err != nil {
		return nil, err
	}

	// Probe the winner, then decide per stream (ADR 0050). This is where a 4K
	// HEVC release with four E-AC3 audio tracks becomes "copy the video, encode
	// the English audio" rather than either a whole-file transcode or a silent
	// film. The plan travels sealed inside the ticket, so the origin does not
	// re-probe on every range request a seeking player makes.
	info, probed := h.mediaInfo(ctx, caller, res)
	plan := playback.Plan{DirectPlay: true}
	if probed {
		plan = playback.Decide(info, profile.codecs(), nil)
	}

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
		telemetry.String("reason", plan.Reason),
		// Which profile the choice was made against. Two clients asking for the
		// same item can correctly get different releases, and without this the
		// second one reads as the first one having gone wrong.
		telemetry.String("capability_class", profile.class),
		// Whether the aggregator was asked at all (ADR 0049). A cache that has
		// silently stopped hitting is indistinguishable from one that was never
		// warm — both just look like playback being slow.
		telemetry.Bool("cached", res.Cached),
		// Whether the bytes had to be probed again (ADR 0050). Once the
		// aggregator call was cached this became the largest remaining cost
		// between a click and a first frame, so it is the number to watch.
		telemetry.Bool("probe_reused", probed && len(res.Probe) > 0))
	ticket, err := h.tickets.Mint(res.URL, res.Headers, caller.Session, plan)
	if err != nil {
		return nil, contracts.WrapError(contracts.Internal, "mint playback ticket", err)
	}

	// Where this viewer got to (ADR 0046). Read after the ticket is minted
	// rather than before, because a resume offset is a refinement and minting
	// is the thing that can fail — ordering it this way means a state read that
	// goes wrong costs the offset, never the play.
	var resumeAt float64
	if !env.Restart {
		resumeAt = h.resumeFor(ctx, caller, env.NodeID).Seconds()
	}

	node := screens.PlayerNode(screens.PlayerParams{
		Src:    "/playback/" + ticket,
		Title:  env.Title,
		Poster: env.Poster,
		NodeID: env.NodeID,
		PartID: string(res.PartID),

		ResumeAt: resumeAt,
		// Anything ffmpeg produces is fragmented MP4; naming the type lets a
		// client pick its pipeline before it fetches a byte. A relayed stream
		// keeps whatever the upstream sends, which the client discovers from
		// the response rather than being told up front.
		MimeType: playbackMimeType(plan),
	})
	msgs := []*sessionv1.ServerMessage{regionMsg(playerRegion, sessionv1.RegionUpdate_REPLACE, node)}

	// What to offer after this one (ADR 0047), pushed as a second region update
	// beside the player — the two-lane transport driving the region unprompted,
	// not the client asking. Best-effort and after the mint, so a missing or slow
	// lookup costs the "Next episode" control, never the play.
	if next := h.nextEpisodeUp(ctx, caller, v1.NodeID(env.NodeID)); next != nil {
		label := "Next episode"
		if next.label != "" {
			label = "Next: " + next.label
		}
		button := screens.NextEpisodeNode(label, next.partID, next.nodeID, next.title)
		msgs = append(msgs, regionMsg(playerRegion, sessionv1.RegionUpdate_APPEND, button))
	}
	return msgs, nil
}

// mediaInfo answers what the chosen release actually is, from storage when it is
// already known and from ffprobe when it is not (ADR 0050).
//
// Reusing a stored probe is the whole reason this is worth having. A probe
// describes bytes and bytes do not change, so the second play of a release was
// paying for an answer nobody could have got wrong — and once ADR 0049's cache
// removed the aggregator call, that payment was the largest thing left between a
// click and a first frame.
//
// A probe failure is not a playback failure. Relaying unprobed is exactly what
// happened before probing existed, so an absent ffprobe — or a source that will
// not answer one — degrades to the previous behaviour rather than refusing to
// play. The cost of that fallback is a silent film when the audio turns out to
// be undecodable, which is the bug this exists to fix; it is still better than
// no picture at all.
func (h *Handler) mediaInfo(ctx context.Context, caller v1.Caller, res app.ResolvePlaybackResult) (playback.MediaInfo, bool) {
	if info, ok := playback.Decode(res.Probe); ok {
		return info, true
	}
	if h.prober == nil || !h.prober.Available() {
		return playback.MediaInfo{}, false
	}
	info, err := h.prober.Probe(ctx, res.URL, res.Headers)
	if err != nil {
		return playback.MediaInfo{}, false
	}
	h.recordProbe(ctx, caller, res.PartID, info)
	return info, true
}

// recordProbe stores what the probe learned, so the next play of this release
// skips it.
//
// Best-effort by design, and the failure worth naming is authorisation rather
// than I/O: recording writes to the content graph, so it asks for
// `content.bind`, and a read-only viewer does not have it. That viewer still
// plays — they simply do not warm the cache for anyone, and every one of their
// plays re-probes. It is the correct refusal and the wrong outcome, and the
// missing piece is the system principal (ADR 0017): work that belongs to the
// install rather than to whoever happened to press play.
func (h *Handler) recordProbe(ctx context.Context, caller v1.Caller, partID v1.PartID, info playback.MediaInfo) {
	if partID == "" {
		return
	}
	doc, err := playback.Encode(info)
	if err != nil {
		return
	}
	_, err = h.svc.RecordPartProbe(ctx, app.RecordPartProbeCommand{
		Caller: caller, PartID: partID,
		Container:  info.Container,
		VideoCodec: info.Video.Codec,
		AudioCodec: playback.SummaryAudioCodec(info),
		Width:      info.Video.Width,
		Height:     info.Video.Height,
		HDRFormat:  info.Video.HDRFormat,
		SizeBytes:  info.SizeBytes,
		Probe:      doc,
	})
	if err != nil {
		telemetry.From(ctx).For("playback").Warn("storing the probe failed; this release will be probed again",
			telemetry.Identifier("part", string(partID)),
			telemetry.Err(err))
	}
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

// extensionEnvelope is the install/uninstall action input (ADR 0081): which
// repository a module comes from and its module id. Uninstall reads only the
// module id.
type extensionEnvelope struct {
	Repository string `json:"repository"`
	ModuleID   string `json:"moduleId"`
}

// extensionRefFromInput decodes an installExtension envelope. The Service
// validates that both fields are present, so an empty one becomes an
// InvalidArgument there rather than being second-guessed here.
func extensionRefFromInput(input []byte) (string, string, error) {
	var env extensionEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return "", "", contracts.NewError(contracts.InvalidArgument, "install extension: input is not valid JSON")
		}
	}
	return env.Repository, env.ModuleID, nil
}

// extensionModuleIDFromInput decodes an uninstallExtension envelope down to the
// module id it names.
func extensionModuleIDFromInput(input []byte) (string, error) {
	var env extensionEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return "", contracts.NewError(contracts.InvalidArgument, "uninstall extension: input is not valid JSON")
		}
	}
	return env.ModuleID, nil
}

// browserPreference is what a desktop browser can play, in the shape selection
// ranks on. It is now the *fallback* rather than the answer: a client that
// declares a profile on Attach (ADR 0047) is ranked against what it said, and
// this stands in only for one that declares nothing.
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

// preferenceEnvelope is the setPreference action input: which setting, and what
// to set it to.
//
// The value is json.RawMessage rather than a concrete type because a preference
// is stored uninterpreted (ADR 0058) — expert mode is a boolean, a theme is a
// string, and the transport should not need editing when the second one
// arrives.
type preferenceEnvelope struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

// preferenceFromInput decodes the setPreference envelope.
//
// It does not check the key against a list. A preference is a user's own
// setting and the surfaces that read one apply their own default for anything
// they do not recognise, so an unknown key is inert rather than dangerous —
// and enumerating them here would mean editing the transport to add a checkbox.
func preferenceFromInput(input []byte) (string, []byte, error) {
	var env preferenceEnvelope
	if err := json.Unmarshal(input, &env); err != nil {
		return "", nil, contracts.WrapError(contracts.InvalidArgument, "decode setPreference input", err)
	}
	if env.Key == "" {
		return "", nil, contracts.NewError(contracts.InvalidArgument, "setPreference needs a key")
	}
	return env.Key, env.Value, nil
}
