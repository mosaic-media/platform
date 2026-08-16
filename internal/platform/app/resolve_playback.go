// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"
	"encoding/json"

	"github.com/mosaic-media/platform/internal/platform/contracts"
	"github.com/mosaic-media/platform/internal/platform/domain"
	"github.com/mosaic-media/platform/internal/platform/policy"
	"github.com/mosaic-media/platform/internal/platform/telemetry"
	v1 "github.com/mosaic-media/sdk/contracts/platform/v1"
)

// ResolvePlaybackQuery asks where an item's playable bytes are, right now.
//
// It names a Part rather than a node because a node has no bytes. That Part is
// the item's entry point rather than a verdict: an import stores every release a
// source offered (platform#28), and which of them actually plays is chosen here,
// against the calling client's Prefer (platform#27).
type ResolvePlaybackQuery struct {
	Caller v1.Caller
	PartID v1.PartID
	// Prefer describes what the calling client can play. It is the shape a
	// declared capability profile reduces to (web#4); an empty value means
	// "no preference expressed", and selection falls back to the source's own
	// ranking rather than inventing one.
	Prefer PlaybackPreference
	// CapabilityClass is the stable digest of that same profile, and the key the
	// resolution cache is read and written under (platform#28).
	//
	// It is passed in rather than derived here because the transport is what
	// receives the declaration, and two independent derivations of one digest is
	// how a cache quietly stops hitting. An empty class disables the cache for
	// this call rather than keying everything under one shared bucket, which
	// would serve a phone an answer chosen for a television.
	CapabilityClass string
}

// PlaybackPreference is the client-shaped half of selection: what it can decode,
// and how much it wants to move.
type PlaybackPreference struct {
	// Containers, VideoCodecs and AudioCodecs are the sets the client can play.
	// Empty means unconstrained.
	Containers  map[string]bool
	VideoCodecs map[string]bool
	AudioCodecs map[string]bool
	// HDR reports whether the client can render high dynamic range, which is a
	// different question from whether it decodes the codec carrying it. An HDR
	// stream rendered as SDR comes out wrong, and fixing it means tone-mapping,
	// which means a full video re-encode — the most expensive thing selection can
	// cause.
	HDR bool
	// MaxHeight caps resolution, 0 for uncapped. A phone asking for 2160p is
	// spending bandwidth on pixels it cannot show.
	MaxHeight int
}

// Empty reports whether the preference expresses nothing at all.
//
// HDR is absent from this test on purpose: it is a bool, so "cannot render HDR"
// and "did not say" are the same value, and a client that declared only HDR has
// said nothing selection can act on by itself.
func (p PlaybackPreference) Empty() bool {
	return len(p.Containers) == 0 && len(p.VideoCodecs) == 0 && len(p.AudioCodecs) == 0 && p.MaxHeight == 0
}

// ResolvePlaybackResult is the upstream location a playback provider resolved,
// for the Platform's own origin to relay from (platform#25).
//
// Do not hand this to a client. URL and Headers may carry a debrid credential,
// and keeping them server-side is half the reason the origin exists — the
// transport seals them into a ticket rather than emitting them.
type ResolvePlaybackResult struct {
	// ModuleID is the playback module that resolved this, carried for
	// diagnostics.
	ModuleID string
	// URL is the location to fetch.
	URL string
	// Headers are request headers the URL's origin requires, nil when it can be
	// fetched bare.
	Headers map[string]string

	// What was chosen, and out of how many. Both are reported because a bad
	// choice and a single candidate look identical from outside, and telling
	// them apart otherwise needs a database query.
	PartID     v1.PartID
	Release    string
	VideoCodec string
	AudioCodec string
	Height     int
	Candidates int

	// Cached reports that this answer came from the resolution cache rather than
	// from the source. Without it a cache that has stopped hitting looks exactly
	// like a cache that was never warm.
	Cached bool

	// Probe is the stored probe document for the chosen release (platform#29),
	// empty when it has never been probed. It rides the result rather than being
	// re-read because the transport must not touch a store.
	Probe []byte
}

// ResolvePlayback turns a Part into a playable upstream location by asking the
// installed playback provider (platform#25's RolePlayback). Nothing here writes,
// and nothing here opens a transaction.
//
// It runs at play time, every time. The Part's stored location is what a source
// offered when the item was materialised, and for a debrid link that is a
// short-lived address which has very likely expired — so the Part is an identity
// hint handed to the provider, not an answer read back out of the graph.
func (s *Service) ResolvePlayback(ctx context.Context, q ResolvePlaybackQuery) (ResolvePlaybackResult, error) {
	if q.Caller.Session == "" {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if q.PartID == "" {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.InvalidArgument, "part id is required")
	}

	if _, err := s.enter(ctx, q.Caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return ResolvePlaybackResult{}, err
	}

	if s.parts == nil {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.Unavailable, "no part store configured")
	}
	part, err := s.parts.FindByID(ctx, q.PartID)
	if err != nil {
		return ResolvePlaybackResult{}, err
	}

	// Which release actually plays is chosen here, not at import (platform#27).
	// Import stored the whole set so this choice could be made with the calling
	// client in view; the Part named by the request is the item's entry point,
	// not a verdict.
	candidates, chosen, switched := s.selectPlayable(ctx, part, q.Prefer)
	if switched {
		part = chosen
	}

	// The cache must be read after selection, never before (platform#28).
	// Selection is a free ranking over Parts already in the database and it
	// names which release this client should get; only then is there a key to
	// look up. Reading the cache first would cache the choice as well as the
	// address, and the choice changes whenever the candidate set does.
	if cached, ok := s.cachedResolution(ctx, part, q.CapabilityClass); ok {
		// ModuleID is left empty, accurately: no module was asked. A cached play
		// therefore also works while a playback module is uninstalled or
		// unreachable — a side effect rather than a guarantee, since the entry
		// still dies when its link does.
		return ResolvePlaybackResult{
			URL: cached.URL, Headers: cached.Headers,
			PartID: part.ID, Release: part.EditionLabel,
			VideoCodec: part.VideoCodec, AudioCodec: part.AudioCodec, Height: part.Height,
			Candidates: candidates, Cached: true, Probe: probeAttribute(part),
		}, nil
	}

	entry, ok := s.playbackProvider()
	if !ok {
		// platform#24's inert library: nothing is installed that can consume what
		// materialising created. Reported as such rather than as a failure to
		// play.
		return ResolvePlaybackResult{}, contracts.NewError(contracts.NotFound, "no playback module is installed")
	}

	settings, err := s.readModuleSettings(ctx, entry.ModuleID)
	if err != nil {
		return ResolvePlaybackResult{}, err
	}

	ctx, span := moduleSpan(ctx, entry.ModuleID, "resolve_playback")
	res, err := entry.Provider.Resolve(ctx, v1.PlaybackRequest{
		Caller:   q.Caller,
		Settings: settings,
		Part:     part,
	})
	failSpan(span, err)
	span.End()
	if err != nil {
		return ResolvePlaybackResult{}, contracts.WrapError(contracts.Unavailable, "resolve playback", err)
	}
	if res.Kind != v1.PlaybackDirect {
		// The SDK declares one variant today; a module returning anything else
		// is built against a contract this Platform does not implement, which is
		// a wiring error rather than a source failure.
		return ResolvePlaybackResult{}, contracts.NewError(contracts.Internal, "playback module returned an unsupported resolution kind")
	}
	if res.URL == "" {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.NotFound, "playback module resolved no location for this part")
	}

	s.cacheResolution(ctx, part.ID, q.CapabilityClass, res.URL, res.Headers)

	return ResolvePlaybackResult{
		ModuleID: entry.ModuleID, URL: res.URL, Headers: res.Headers,
		PartID: part.ID, Release: part.EditionLabel,
		VideoCodec: part.VideoCodec, AudioCodec: part.AudioCodec, Height: part.Height,
		Candidates: candidates, Probe: probeAttribute(part),
	}, nil
}

// probeAttribute lifts the stored probe document out of a Part's attributes.
//
// A Part with no probe, or with attributes that will not decode, yields nothing
// and the caller probes afresh. Nothing here is worth failing a play over: the
// worst case is one ffprobe run.
func probeAttribute(part v1.Part) []byte {
	if len(part.Attributes) == 0 {
		return nil
	}
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(part.Attributes, &doc); err != nil {
		return nil
	}
	raw, ok := doc[ProbeAttribute]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return nil
	}
	return raw
}

// cachedResolution reads a previously resolved location for this part and class
// (platform#28).
//
// There is deliberately no liveness check. Pre-checking would spend a round trip
// on every play to catch a rare failure, which is the latency this cache exists
// to remove; a dead entry is discovered by using it and corrected then. See
// ReresolvePlayback.
//
// Every failure here degrades to a miss rather than an error. A cache that
// cannot be read must cost a slow play, never a failed one.
func (s *Service) cachedResolution(ctx context.Context, part v1.Part, class string) (domain.PlaybackResolution, bool) {
	if s.resolutions == nil || class == "" || part.ID == "" {
		return domain.PlaybackResolution{}, false
	}
	res, err := s.resolutions.Get(ctx, string(part.ID), class)
	if err != nil || res.URL == "" {
		return domain.PlaybackResolution{}, false
	}
	return res, true
}

// cacheResolution stores what the source just answered, for the next client of
// the same class to reuse.
//
// It writes on the request's own goroutine rather than in the background.
// platform#28 requires only that the cache write not block the stream, and
// nothing has started streaming yet — the client has not been handed a ticket.
// A write error is logged and swallowed: being unable to make the next play fast
// is not a reason to refuse this one.
func (s *Service) cacheResolution(ctx context.Context, partID v1.PartID, class, url string, headers map[string]string) {
	if s.resolutions == nil || class == "" || partID == "" || url == "" {
		return
	}
	err := s.resolutions.Set(ctx, domain.PlaybackResolution{
		PartID:          string(partID),
		CapabilityClass: class,
		URL:             url,
		Headers:         headers,
		ResolvedAt:      s.clock.Now(),
	})
	if err != nil {
		telemetry.From(ctx).For("playback").Warn("caching the resolved location failed",
			telemetry.Identifier("part", string(partID)),
			telemetry.String("capability_class", class),
			telemetry.Err(err))
	}
}

// playbackProvider picks the playback provider to resolve through, tolerating a
// Service built without a registry.
//
// It takes the first in stable module-id order. Precedence between two installed
// playback modules is undecided — the consumer-side twin of sdk#2's open
// provider-precedence seam — and should be settled with that one rather than
// invented here.
func (s *Service) playbackProvider() (PlaybackProviderEntry, bool) {
	if s.capabilities == nil {
		return PlaybackProviderEntry{}, false
	}
	entries := s.capabilities.PlaybackProviders()
	if len(entries) == 0 {
		return PlaybackProviderEntry{}, false
	}
	return entries[0], true
}

// selectPlayable picks the candidate to play from the item's Parts.
//
// A candidate the client can decode outright beats one needing work, because
// re-encoding costs latency the viewer sees and forfeits byte-range seeking
// (platform#27). Among equals the source's own ranking wins.
//
// It is best-effort by construction: the metadata it ranks on was parsed from
// release text at the module boundary (module-stremio-addons#2) and can be wrong
// or absent. That is acceptable because it only orders a list — what the chosen
// release actually contains is settled by probing the bytes before they play
// (platform#29), so a bad parse costs a suboptimal choice rather than a failed
// play.
//
// It returns how many candidates it had to choose from, which is the difference
// between "ranking picked this" and "there was nothing else".
func (s *Service) selectPlayable(ctx context.Context, entry v1.Part, prefer PlaybackPreference) (int, v1.Part, bool) {
	if entry.NodeID == "" || s.parts == nil {
		return 1, entry, false
	}
	candidates, err := s.parts.ListByNode(ctx, entry.NodeID)
	if err != nil || len(candidates) < 2 {
		return len(candidates), entry, false
	}

	best, bestScore := entry, playbackScore(entry, prefer)
	for _, c := range candidates {
		if c.ID == entry.ID {
			continue
		}
		if score := playbackScore(c, prefer); score > bestScore {
			best, bestScore = c, score
		}
	}
	return len(candidates), best, best.ID != entry.ID
}

// codecScore rates one codec against what a client can decode: known-good,
// unknown, or known-bad, in that order.
func codecScore(codec string, accepted map[string]bool) int {
	if len(accepted) == 0 || codec == "" {
		return 0 // nothing to judge against, or nothing parsed to judge
	}
	if accepted[codec] {
		return 1000
	}
	return -1000
}

// playbackScore ranks one candidate for a client. Higher is better.
//
// The weights encode an order rather than a measurement: compatibility dominates
// resolution, because an unplayable 4K release is worth less than a playable
// 720p one.
func playbackScore(p v1.Part, prefer PlaybackPreference) int {
	score := 0

	if prefer.Empty() {
		// Nothing to rank against, so keep the source's order: a lower
		// NaturalOrder (the addon's own ranking) scores higher.
		return -int(p.NaturalOrder)
	}

	// Codec compatibility first, and audio counts as much as video: an
	// undecodable audio track is the difference between a film and a silent one.
	//
	// Three states, not two. A codec the client is known to decode is rewarded;
	// one it is known not to decode is penalised; one the module could not parse
	// is left neutral. Do not collapse the last two: the module's parse is
	// best-effort, so plenty of playable releases arrive unparsed, and ranking
	// them as known-bad would bury them.
	score += codecScore(p.VideoCodec, prefer.VideoCodecs)
	score += codecScore(p.AudioCodec, prefer.AudioCodecs)
	if len(prefer.Containers) > 0 && p.Container != "" && prefer.Containers[p.Container] {
		score += 200
	}

	// HDR the client cannot render means a video re-encode, the most expensive
	// outcome selection can produce, so it is penalised harder than an audio
	// mismatch. It sits below outright undecodability because a tone-mapped HDR
	// release does eventually play, where an undecodable one never does.
	if !prefer.HDR && p.HDRFormat != "" {
		score -= 400
	}

	// Resolution, once compatibility is settled. A capped client gains nothing
	// from pixels it cannot display and pays for them in bandwidth.
	switch {
	case prefer.MaxHeight > 0 && p.Height > prefer.MaxHeight:
		score -= 500
	case p.Height > 0:
		score += p.Height / 10
	}

	// The source's own ranking breaks ties, faintly enough not to outweigh
	// anything above it.
	score -= int(p.NaturalOrder)
	return score
}

// ReresolvePlayback asks the source again where a release's bytes are, and
// overwrites the cached answer (platform#28's invalidate-on-read).
//
// It must not read the cache. The origin calls this precisely because the cached
// address did not work, so consulting it would return the dead link that
// prompted the call. The write at the end overwrites rather than deletes: the
// candidate was never wrong, only its address changed.
//
// It runs as the session that minted the ticket, so it authorises on the same
// terms as the play that produced it. That is why the origin can call it at all:
// it re-runs the boundary rather than trusting the ticket as proof of anything
// but its own provenance.
//
// It resolves the Part it is given and must not re-run selection. Selection
// already chose this release for this client; re-choosing mid-playback would
// change the film under a viewer thirty minutes in.
func (s *Service) ReresolvePlayback(ctx context.Context, caller v1.Caller, partID v1.PartID, class string) (ResolvePlaybackResult, error) {
	if caller.Session == "" {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.InvalidArgument, "caller is required")
	}
	if partID == "" {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.InvalidArgument, "part id is required")
	}
	if _, err := s.enter(ctx, caller, ActionContentRead, policy.Resource{Type: "content"}); err != nil {
		return ResolvePlaybackResult{}, err
	}
	if s.parts == nil {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.Unavailable, "no part store configured")
	}
	part, err := s.parts.FindByID(ctx, partID)
	if err != nil {
		return ResolvePlaybackResult{}, err
	}

	entry, ok := s.playbackProvider()
	if !ok {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.NotFound, "no playback module is installed")
	}
	settings, err := s.readModuleSettings(ctx, entry.ModuleID)
	if err != nil {
		return ResolvePlaybackResult{}, err
	}

	ctx, span := moduleSpan(ctx, entry.ModuleID, "reresolve_playback")
	res, err := entry.Provider.Resolve(ctx, v1.PlaybackRequest{
		Caller: caller, Settings: settings, Part: part,
	})
	failSpan(span, err)
	span.End()
	if err != nil {
		return ResolvePlaybackResult{}, contracts.WrapError(contracts.Unavailable, "resolve playback", err)
	}
	if res.Kind != v1.PlaybackDirect || res.URL == "" {
		return ResolvePlaybackResult{}, contracts.NewError(contracts.NotFound, "playback module resolved no location for this part")
	}

	s.cacheResolution(ctx, part.ID, class, res.URL, res.Headers)

	return ResolvePlaybackResult{
		ModuleID: entry.ModuleID, URL: res.URL, Headers: res.Headers,
		PartID: part.ID, Release: part.EditionLabel,
		VideoCodec: part.VideoCodec, AudioCodec: part.AudioCodec, Height: part.Height,
		Probe: probeAttribute(part),
	}, nil
}
