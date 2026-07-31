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
	"github.com/mosaic-media/platform/internal/platform/domain"
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
	caller := s.currentCaller()
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
	case "revokeSession":
		// Ending one device's session (ADR 0102). The target is a session id
		// from the device list; the caller is this connection's own credential,
		// and the command's own boundary decides whether they may — a viewer
		// ending their own phone and an administrator ending somebody else's
		// TV are the same call and a different authorisation.
		target, err := sessionIDFromInput(input)
		if err != nil {
			return nil, err
		}
		if _, err := h.svc.RevokeSession(ctx, app.RevokeSessionCommand{
			CallerSessionID: domain.SessionID(caller.Session),
			TargetSessionID: target,
		}); err != nil {
			return nil, err
		}
		// And end that device's live session, so the revocation reaches it now
		// rather than when its access token happens to expire. The target is
		// somebody else's connection, which is exactly why the Manager is keyed
		// by session id: this call has no other handle on it.
		h.mgr.End(string(target))
		return nil, nil
	case "signOut":
		// Ending *this* session (ADR 0102). It names no target and cannot: the
		// session it arrives on is the one it revokes, which is what makes it
		// safe to put on the account cluster of every screen — an affordance
		// that could be pointed at another device would need to say which.
		//
		// The client discovers the outcome the way it discovers any revocation:
		// its next call fails Unauthenticated, it drops the stored pair, and the
		// doorway comes back. That is the same path a refused session already
		// took, which is why signing out and being signed out are one code path
		// in the client rather than two.
		if _, err := h.svc.RevokeSession(ctx, app.RevokeSessionCommand{
			CallerSessionID: domain.SessionID(caller.Session),
			TargetSessionID: domain.SessionID(s.ref),
		}); err != nil {
			return nil, err
		}
		// End the live session too, after the revocation and never before: the
		// credential is what actually ended, and closing the stream first would
		// drop a client that was still signed in if the command then failed.
		h.mgr.End(s.ref)
		return nil, nil
	case "createAccount":
		// Provisioning a household member (ADR 0069). Three commands behind one
		// action — see accounts.go for why they are three and what a failure
		// between them leaves behind.
		return nil, h.createAccount(ctx, caller, input)
	case "setUserStatus":
		return nil, h.setUserStatus(ctx, caller, input)
	case "grantPreset":
		return nil, h.grantPreset(ctx, caller, input)
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
	case "recordImpression":
		// What a screen reported having been *seen* (ADR 0090). It is silent for
		// the same reason reportProgress is — a toast per card scrolled past is
		// noise over the screen it is about — and it is the only consumer of the
		// lifecycle triggers today.
		//
		// The Platform records it and stores nothing. Telemetry is where an
		// impression belongs until there is a question worth keeping one to
		// answer; a table filled first and queried never is a retention
		// liability rather than an analytics capability.
		return nil, h.recordImpression(ctx, s, input)
	case "createLibraryRule":
		// What the library should contain (ADR 0104). The four library-rule
		// cases each return their own surface — the rules list — because the
		// panel they are invoked from has stopped describing anything true the
		// moment they succeed.
		return h.createLibraryRule(ctx, s, caller, input)
	case "setLibraryRuleEnabled":
		return h.setLibraryRuleEnabled(ctx, s, caller, input)
	case "deleteLibraryRule":
		return h.deleteLibraryRule(ctx, s, caller, input)
	case "runLibraryMaintenance":
		return h.runLibraryMaintenance(ctx, s, caller)
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
	caller := s.currentCaller()
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
	// What this viewer wants to hear (ADR 0112). Read here rather than baked
	// into the Platform, because language belongs to a person: four people
	// sharing one library previously got one person's answer from a package
	// variable, and the parameter that would have carried theirs was passed nil.
	langs := playback.ParseLanguagePreference(h.svc.LanguagePreferenceFor(ctx, caller))

	info, probed := h.mediaInfo(ctx, caller, res)
	plan := playback.Plan{DirectPlay: true}
	if probed {
		plan = playback.Decide(info, profile.codecs(), langs.Audio)
	}
	// What they should read, after the release has had its say. A preference
	// names what someone wants when they got the language they asked for; when
	// they did not, this is where the Platform notices and escalates.
	subtitles := langs.SubtitlesFor(plan.AudioLanguage)
	// Offered as HLS renditions, which is why subtitles cost no client change
	// (ADR 0113). Only on the transcoded path: a direct-played release is
	// relayed byte for byte, so there is no playlist to declare a rendition in.
	//
	// A track that cannot be a rendition is burned into the picture instead
	// (ADR 0114), which forces a video encode — so it can turn a release that
	// would have direct-played into one that does not. That is the whole cost of
	// typeset fidelity and of a Blu-ray's picture subtitles, and it is why the
	// preference behind it is opt-in.
	if probed && !plan.DirectPlay {
		plan.Subtitles, plan.Burn = playback.DecideSubtitles(info.Subtitles, subtitles, langs.Typeset)
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
		telemetry.Bool("probe_reused", probed && len(res.Probe) > 0),
		// Which language they actually got, and whether the subtitle mode had to
		// be raised because it was not the one they asked for. Both are
		// invisible from the outside — a viewer sees a film in a language and
		// cannot tell whether the Platform chose it or settled for it.
		telemetry.String("audio_language", plan.AudioLanguage),
		telemetry.String("subtitle_mode", string(subtitles.Mode)),
		telemetry.Bool("subtitle_escalated", subtitles.Escalated),
		// Whether subtitles forced a video encode (ADR 0114). This is the single
		// most expensive thing a playback can decide to do, and from the outside
		// it presents only as a release that suddenly plays badly — so the log
		// has to be able to answer "was it the subtitles".
		telemetry.Bool("subtitle_burned", plan.Burn != nil))
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
		Src:    playbackSrc(ticket, plan),
		Title:  env.Title,
		Poster: env.Poster,
		NodeID: env.NodeID,
		PartID: string(res.PartID),

		ResumeAt: resumeAt,
		// Which pipeline the client should use, decided before it fetches a
		// byte. A release that goes through ffmpeg is served as HLS and needs a
		// media framework; a relayed one keeps whatever the upstream sends and
		// the client discovers that from the response.
		MimeType: playbackMimeType(plan),
	})
	msgs := []*sessionv1.ServerMessage{regionMsg(ctx, s, playerRegion, sessionv1.RegionUpdate_REPLACE, node)}

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
		msgs = append(msgs, regionMsg(ctx, s, playerRegion, sessionv1.RegionUpdate_APPEND, button))
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
// **It records as the system principal, not as the viewer** (ADR 0017).
// Recording writes to the content graph, so it asks for `content.bind`, and a
// read-only viewer does not have it — so this used to be a correct refusal with
// a wrong outcome: that viewer played, warmed the cache for nobody, and
// re-probed on every play forever. What is being recorded is a fact about a
// file. It would be identical whoever pressed play, it is not about the person,
// and the install is the one that wants it kept. That makes it exactly the
// no-user work the system principal exists for, and the viewer's own authority
// to *play* is unchanged and was checked before this is reached.
//
// Still best-effort: a storage failure must not fail a playback that has
// already resolved.
func (h *Handler) recordProbe(ctx context.Context, _ v1.Caller, partID v1.PartID, info playback.MediaInfo) {
	if partID == "" {
		return
	}
	doc, err := playback.Encode(info)
	if err != nil {
		return
	}
	_, err = h.svc.RecordPartProbe(ctx, app.RecordPartProbeCommand{
		Caller: h.svc.SystemCaller(), PartID: partID,
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
	if plan.DirectPlay {
		// The upstream's own container, whatever it is. A bare <video> reads it
		// from the response, and naming it here would be a guess.
		return ""
	}
	if plan.Duration <= 0 {
		// No duration means no playlist, so the origin serves the unseekable
		// fragmented-MP4 pipe and the client plays it as a plain progressive
		// stream (ADR 0111).
		return "video/mp4"
	}
	return playback.HLSMimeType
}

// playbackSrc is what the client fetches.
//
// A relayed stream is the ticket itself; a segmented one is the playlist beneath
// it (ADR 0109). The two are different resources rather than the same one
// behaving differently, so the difference is in the URL rather than in a header
// the client would have to fetch before it knew what it had.
func playbackSrc(ticket string, plan playback.Plan) string {
	if !plan.DirectPlay && plan.Duration > 0 {
		return "/playback/" + ticket + "/" + playback.PlaylistName
	}
	return "/playback/" + ticket
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

// impressionEnvelope is what a screen's onAppear action carries. The fields are
// the server's own — it wrote the action — so this is reading back what the
// emit-side put there rather than trusting a client to describe itself.
type impressionEnvelope struct {
	// NodeID is the node's `id`, which the contract states is the analytics
	// identity as well as the React key. An id that is a row index attributes
	// nothing; one that names the thing attributes everything, and only the
	// emit-side knows which it wrote.
	NodeID string `json:"nodeId"`
	Screen string `json:"screen"`
	Kind   string `json:"kind"`
}

// recordImpression writes one "this was seen" record to telemetry.
//
// It deliberately does not authorise beyond the session the report arrived on:
// an impression is a statement about the caller's own screen, and there is no
// object it could name that they were not already looking at.
func (h *Handler) recordImpression(ctx context.Context, s *liveSession, input []byte) error {
	var env impressionEnvelope
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return contracts.WrapError(contracts.InvalidArgument, "impression", err)
		}
	}
	if env.NodeID == "" {
		// A report that names nothing attributes nothing, and recording it would
		// inflate a count while answering no question.
		return contracts.NewError(contracts.InvalidArgument, "an impression must name the node it saw")
	}
	telemetry.From(ctx).Info("sdui impression",
		telemetry.Identifier("session", s.ref),
		telemetry.String("node", env.NodeID),
		telemetry.String("screen", env.Screen),
		telemetry.String("kind", env.Kind))
	return nil
}

// sessionIDFromInput reads the session a revokeSession action names.
//
// The value is a session id and not a credential — it is what a device list
// shows and what revocation targets — so passing one here reveals nothing and
// grants nothing on its own: the command authorises the caller before it acts.
func sessionIDFromInput(input []byte) (domain.SessionID, error) {
	var env struct {
		SessionID string `json:"sessionId"`
	}
	if len(input) > 0 {
		if err := json.Unmarshal(input, &env); err != nil {
			return "", contracts.NewError(contracts.InvalidArgument, "revoke session: input is not valid JSON")
		}
	}
	if env.SessionID == "" {
		return "", contracts.NewError(contracts.InvalidArgument, "revoke session: a session id is required")
	}
	return domain.SessionID(env.SessionID), nil
}
