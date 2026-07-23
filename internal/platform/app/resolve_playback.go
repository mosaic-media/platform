// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors
// Linking exception: see LICENSE-EXCEPTION.

package app

import (
	"context"

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
// source offered (ADR 0049), and which of them actually plays is chosen here,
// against the calling client's Prefer (ADR 0048).
type ResolvePlaybackQuery struct {
	Caller v1.Caller
	PartID v1.PartID
	// Prefer describes what the calling client can play. It is the shape a
	// declared capability profile reduces to (ADR 0047); an empty value means
	// "no preference expressed", and selection falls back to the source's own
	// ranking rather than inventing one.
	Prefer PlaybackPreference
	// CapabilityClass is the stable digest of that same profile, and the key the
	// resolution cache is read and written under (ADR 0049).
	//
	// It is passed in rather than derived here because deriving it is the
	// transport's job — the transport is what receives the declaration — and two
	// independent derivations of one digest is exactly how a cache quietly stops
	// hitting. An empty class disables the cache for this call rather than
	// keying everything under one shared bucket, which would serve a phone an
	// answer chosen for a television.
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
	// HDR reports whether the client can *render* high dynamic range, which is a
	// different question from whether it decodes the codec carrying it. A client
	// that cannot is not merely missing an enhancement: an HDR stream rendered as
	// SDR comes out wrong, and fixing it means tone-mapping, which means a full
	// video re-encode — the most expensive thing selection can cause.
	HDR bool
	// MaxHeight caps resolution, 0 for uncapped. A phone asking for 2160p is
	// spending bandwidth on pixels it cannot show.
	MaxHeight int
}

// Empty reports whether the preference expresses nothing at all.
//
// HDR is absent from this test on purpose. It is a bool, so "cannot render HDR"
// and "did not say" are the same value, and a client that declared only
// `hdr: true` has said nothing selection can act on by itself.
func (p PlaybackPreference) Empty() bool {
	return len(p.Containers) == 0 && len(p.VideoCodecs) == 0 && len(p.AudioCodecs) == 0 && p.MaxHeight == 0
}

// ResolvePlaybackResult is the upstream location a playback provider resolved,
// for the Platform's own origin to relay from (ADR 0045).
//
// It is deliberately *not* something to hand a client. URL and Headers may
// carry a debrid credential, and keeping them server-side is half the reason
// the origin exists — the transport seals them into a ticket rather than
// emitting them.
type ResolvePlaybackResult struct {
	// ModuleID is the playback module that resolved this, carried for
	// diagnostics.
	ModuleID string
	// URL is the location to fetch.
	URL string
	// Headers are request headers the URL's origin requires, nil when it can be
	// fetched bare.
	Headers map[string]string

	// What was chosen, and out of how many. Selection is invisible when it works
	// and indistinguishable from a bug when it does not — "nothing changed"
	// looks identical whether ranking picked badly or the item only ever had one
	// candidate to pick from. Reporting both makes that answerable without a
	// database query.
	PartID     v1.PartID
	Release    string
	VideoCodec string
	AudioCodec string
	Height     int
	Candidates int

	// Cached reports that this answer came from the resolution cache rather than
	// from the source. It is the one field that distinguishes a fast play from a
	// slow one after the fact, and without it a cache that has silently stopped
	// hitting looks exactly like a cache that was never warm.
	Cached bool
}

// ResolvePlayback turns a Part into a playable upstream location by asking the
// installed playback provider (ADR 0045's RolePlayback). Nothing here writes,
// and nothing here opens a transaction.
//
// It runs at play time, every time. The Part's stored location is what a source
// offered when the item was materialised, and for a debrid link that is a
// short-lived address which has very likely expired — so the Part is an
// identity hint handed to the provider, not an answer read back out of the
// graph.
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

	// Which release actually plays is chosen here, not at import (ADR 0048).
	// The source offers dozens for one item and they differ in ways that decide
	// whether a given client can play them at all; import stored the set
	// precisely so this choice could be made with the caller in view. The Part
	// named by the request is the item's entry point, not a verdict.
	candidates, chosen, switched := s.selectPlayable(ctx, part, q.Prefer)
	if switched {
		part = chosen
	}

	// The cache is read after selection, not before it, and the order is the
	// whole point (ADR 0049). Selection is a ranking over Parts already in the
	// database — free, and it names *which* release this client should get. Only
	// then is there a key to look up. Reading the cache first would mean caching
	// the choice as well as the address, and the choice is cheap to remake and
	// changes whenever the candidate set does.
	if cached, ok := s.cachedResolution(ctx, part, q.CapabilityClass); ok {
		// ModuleID is left empty, and that is the accurate report: no module was
		// asked. A cached play therefore also works while a playback module is
		// uninstalled or unreachable, which is a side effect rather than a
		// designed guarantee — the entry still dies when its link does.
		return ResolvePlaybackResult{
			URL: cached.URL, Headers: cached.Headers,
			PartID: part.ID, Release: part.EditionLabel,
			VideoCodec: part.VideoCodec, AudioCodec: part.AudioCodec, Height: part.Height,
			Candidates: candidates, Cached: true,
		}, nil
	}

	entry, ok := s.playbackProvider()
	if !ok {
		// This is ADR 0036's inert library, reported honestly rather than as a
		// failure to play: nothing is installed that can consume what
		// materialising created.
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
		Candidates: candidates,
	}, nil
}

// cachedResolution reads a previously resolved location for this part and class
// (ADR 0049).
//
// There is deliberately no liveness check. Pre-checking would spend a round trip
// on every single play to catch a failure that is rare — which is the exact
// latency this cache exists to remove — so a dead entry is discovered by using
// it and corrected then.
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
// It writes on the request's own goroutine rather than in the background, and
// that is a smaller compromise than it looks: ADR 0049's requirement is that the
// cache write must not block the *stream*, and nothing has started streaming
// yet — the client has not even been handed a ticket. What it must not do is
// fail the play, so a write error is logged and swallowed. Being unable to make
// the next play fast is not a reason to refuse this one.
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
// It takes the first in stable module-id order. That is a real choice and worth
// naming: precedence *between* two installed playback modules is undecided, and
// with one installed the question does not arise. It is the consumer-side twin
// of ADR 0027's open provider-precedence seam, and it should be settled with
// that one rather than invented here.
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
// The ordering is deliberate and is the whole of ADR 0048's argument. A
// candidate the client can decode outright beats one needing work, because
// re-encoding costs latency the viewer sees and forfeits byte-range seeking.
// Among equals, the source's own ranking wins — it knows its ecosystem better
// than a guess made here does.
//
// It is best-effort by construction: the metadata it ranks on was parsed from
// release text at the module boundary (ADR 0051) and can be wrong or absent.
// That is acceptable *because* it only orders a list — what the chosen release
// actually contains is settled by probing the bytes before they play (ADR 0050),
// so a bad parse costs a suboptimal choice rather than a failed play.
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
// 720p one, and a needed re-encode is a real cost rather than a footnote.
func playbackScore(p v1.Part, prefer PlaybackPreference) int {
	score := 0

	if prefer.Empty() {
		// Nothing to rank against, so keep the source's order: a lower
		// NaturalOrder (the addon's own ranking) scores higher.
		return -int(p.NaturalOrder)
	}

	// Codec compatibility first, and audio counts as much as video: an
	// undecodable audio track is the difference between a film and a silent
	// film, which is not a lesser failure.
	//
	// Three states, not two, and the distinction is load-bearing. A codec the
	// client is known to decode is rewarded; one it is known *not* to decode is
	// penalised; and one the module could not parse is left neutral. Collapsing
	// the last two would rank an unparsed candidate as though it were known-bad
	// — and the module's parse is best-effort, so plenty of perfectly playable
	// releases arrive unparsed. Unknown is not the same as wrong.
	score += codecScore(p.VideoCodec, prefer.VideoCodecs)
	score += codecScore(p.AudioCodec, prefer.AudioCodecs)
	if len(prefer.Containers) > 0 && p.Container != "" && prefer.Containers[p.Container] {
		score += 200
	}

	// HDR the client cannot render is a video re-encode, and a video re-encode is
	// the most expensive outcome selection can produce — so it is penalised harder
	// than an audio mismatch, which costs almost nothing to fix. It sits below
	// outright undecodability because a tone-mapped HDR release does eventually
	// play, where an undecodable one never does.
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
