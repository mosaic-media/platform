// SPDX-License-Identifier: AGPL-3.0-only
// SPDX-FileCopyrightText: 2026 the Mosaic authors

package session

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strconv"
	"strings"

	sessionv1 "github.com/mosaic-media/contracts/gen/mosaic/session/v1"
	"github.com/mosaic-media/platform/internal/platform/app"
	"github.com/mosaic-media/platform/internal/transport/playback"
)

// The client capability profile, and the class it reduces to (ADR 0047, 0049).
//
// A client declares what it can decode on Attach. Before that existed the
// Platform hard-coded a desktop browser's abilities at the playPart call site,
// which was honest for one client and would have been a lie for the four the
// transport was built to serve.
//
// The *class* is the second half. Stream resolution is expensive — it costs the
// aggregator round trip that dominates the whole latency budget — so its result
// is cached, and the cache has to be keyed by something. Not by user: two people
// with the same television want the same answer, and keying by user would store
// it twice. Not by device either: five identical phones are one answer. What
// actually determines the result is *what the client can decode*, so that is
// what the key is.

// clientProfile is a declared profile plus the class it hashes to. Both are
// derived once, on Attach, rather than per playback: the declaration cannot
// change without a reconnect, so recomputing it per play would be work with a
// constant answer.
type clientProfile struct {
	prefer app.PlaybackPreference
	class  string
	// declared distinguishes "this client said it can play nothing" from "this
	// client said nothing at all". The first is a real answer to respect; the
	// second is an old client, and falls back to the assumption made before the
	// field existed.
	declared bool
}

// profileFrom converts a declared profile into the preference selection ranks on
// and the class the resolution cache keys on.
func profileFrom(p *sessionv1.ClientProfile) clientProfile {
	if p == nil {
		return clientProfile{prefer: browserPreference(), class: legacyBrowserClass}
	}
	prefer := app.PlaybackPreference{
		Containers:  codecSet(p.GetContainers()),
		VideoCodecs: codecSet(p.GetVideoCodecs()),
		AudioCodecs: codecSet(p.GetAudioCodecs()),
		HDR:         p.GetHdr(),
		MaxHeight:   int(p.GetMaxHeight()),
	}
	return clientProfile{prefer: prefer, class: capabilityClass(prefer), declared: true}
}

// defaultEncodeHeight caps re-encoded video when the client named no cap.
//
// An uncapped *display* is not an argument for an uncapped *encode*. The two
// numbers answer different questions: what the panel can show, and what a home
// server's software encoder can produce in real time while a viewer waits. The
// second is the smaller of the two on any machine Mosaic is likely to run on, so
// a client that declares no ceiling still gets one here.
const defaultEncodeHeight = 1080

// codecs is the same profile in the shape the per-stream decision takes
// (ADR 0050). The two types exist separately because they answer different
// questions — one ranks candidates, the other plans ffmpeg — and collapsing them
// would tie the decision to selection's notion of a preference.
func (c clientProfile) codecs() playback.ClientCodecs {
	if !c.declared {
		return playback.DefaultBrowserCodecs
	}
	height := c.prefer.MaxHeight
	if height == 0 {
		height = defaultEncodeHeight
	}
	return playback.ClientCodecs{
		Video:     c.prefer.VideoCodecs,
		Audio:     c.prefer.AudioCodecs,
		HDR:       c.prefer.HDR,
		MaxHeight: height,
	}
}

// legacyBrowserClass names the class of a client that declared nothing.
//
// It is a real class rather than an empty string so such clients still share a
// cache entry with each other — they are, after all, being treated identically —
// while never sharing one with a client that actually declared the same
// abilities. A guess and a declaration should not collide in the cache even when
// they happen to agree, because only one of them is known to be true.
const legacyBrowserClass = "assumed-browser"

// codecSet turns a declared list into the set selection ranks against,
// lowercased so a client shouting "HEVC" matches a probe reporting "hevc".
func codecSet(in []string) map[string]bool {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]bool, len(in))
	for _, s := range in {
		if s = strings.ToLower(strings.TrimSpace(s)); s != "" {
			out[s] = true
		}
	}
	return out
}

// capabilityClass reduces a profile to a stable digest.
//
// Stability is the whole requirement, and it is easy to lose: two clients
// declaring the same codecs in a different order must land in the same class, or
// the cache fragments silently and its only symptom is never seeming warm. So
// every set is sorted before it is hashed.
//
// The digest is truncated because it is a cache key and a log line, not a
// security boundary — a collision costs one client a resolution meant for
// another with identical abilities, which is the same answer.
func capabilityClass(p app.PlaybackPreference) string {
	var b strings.Builder
	b.WriteString("c=")
	b.WriteString(strings.Join(sortedKeys(p.Containers), ","))
	b.WriteString(";v=")
	b.WriteString(strings.Join(sortedKeys(p.VideoCodecs), ","))
	b.WriteString(";a=")
	b.WriteString(strings.Join(sortedKeys(p.AudioCodecs), ","))
	b.WriteString(";hdr=")
	b.WriteString(strconv.FormatBool(p.HDR))
	b.WriteString(";h=")
	b.WriteString(strconv.Itoa(p.MaxHeight))

	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:8])
}

// sortedKeys returns a set's members in a stable order.
func sortedKeys(m map[string]bool) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
