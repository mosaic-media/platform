# The artwork proxy (and cache)

**Status:** Accepted
**Date:** 2026-07-20

## Context

Module metadata carries poster and backdrop URLs that point at third-party
CDNs — Cinemeta's art lives on `images.metahub.space`, other sources elsewhere.
The first cut of the SDUI emit-side ([platform#19](0019-sdui-emit-side.md)) passed
those URLs straight to the client, which fetched them directly from the CDN.
Driving the Shell against real addons showed three problems with that:

1. **The artlight canvas needs CORS.** The SDUI runtime samples an image's
   colours into a canvas to drive its ambient "refraction" wash. Reading a
   cross-origin image's pixels is a browser security boundary — it works only
   if the CDN sends `Access-Control-Allow-Origin`. metahub does not, so the
   sampling silently fails; worse, the runtime requested art with
   `crossOrigin="anonymous"` to *enable* sampling, and a CORS-less response to
   that request makes the browser **drop the image entirely** — broken art on
   every card and detail header sourced from such a CDN.
2. **Reliability track s the CDN.** A slow or down CDN is a slow or broken
   library.
3. **Privacy.** Every poster load leaks the viewer's IP to a third party.

No client-side trick fixes the CORS one: the bytes have to come from an origin
the Platform controls. This is what every media server (Jellyfin, Plex, Emby)
does — it never lets the client hit the metadata CDN directly.

## Decision

**The Platform re-serves artwork from its own origin. A client is only ever
handed a Platform artwork URL, never a CDN URL. It splits along the two content
planes ([platform#18](0018-virtual-and-materialized-content.md)): browse-time
artwork is *proxied* and stored nowhere; a materialised item's chosen artwork
is *cached durably*.**

**Artwork is a cacheable presentational asset — a clarification to
[platform#10](0010-storage-authority-and-transaction-scope.md), not a breach of
it.** That ADR's "media is linked, never absorbed" governs a Work's *primary
bytes* (the movie, the stream), which Mosaic must never copy. A poster is small,
derived, presentational; caching it does not make Mosaic a media store. Primary
media stays linked; presentational assets the Platform may hold.

**Slice 1 — the proxy (built).** A `GET /artwork` endpoint fetches a remote
image and streams it back with permissive CORS. The emit-side rewrites every
poster/backdrop URL through it, emitting a **relative** `/artwork?…` URL — so
the client fetches it same-origin and the artlight canvas is readable *with no
CORS at all*. Nothing is stored: this is the virtual plane, so uncurated art
never accumulates (an in-memory cache is a later optimisation). An open `?url=`
proxy is made safe two ways — every emitted URL is **HMAC-signed** (a
process-scoped key; screens are re-fetched, so signatures need not outlive the
process), so the proxy fetches only URLs it produced; and the dialer **refuses
loopback, private and link-local targets at connect time** (after DNS, so a
rebinding trick cannot slip past), closing the SSRF hole. Images only,
size-capped, timeout-bounded.

**Slice 2 — the durable cache (planned).** When an item is materialised, the
Platform picks the top artwork candidate and caches it on a **filesystem blob
directory** keyed by content hash — bounded by curation, exactly as the stream
part is snapshotted. Metadata sources return *several* artwork URLs (Cinemeta
alone gives poster, background, logo); the module surfaces the candidates and
the Platform selects. This removes the runtime CDN dependency for library
content entirely.

## Alternatives considered

**Let the client fetch the CDN directly (status quo).** *Rejected:* the CORS
boundary is not the client's to cross, and it leaks the viewer's IP. The client
retains a display fallback for a genuinely-missing image, but it is defence in
depth, not the mechanism.

**Durably cache every artwork the client sees.** *Rejected:* caching browse-time
art floods storage with uncurated assets — the exact thing [platform#18](0018-virtual-and-materialized-content.md)'s
two planes prevent. Browse proxies; only curation caches.

**Store cached artwork in Postgres (bytea / large objects).** *Rejected for the
durable cache:* it bloats the database with binaries and serves slower than a
static file. The filesystem blob directory is how media servers do it and keeps
large binaries out of the transactional store.

**No proxy — set permissive image headers and hope.** *Rejected:* the Platform
does not control the CDN's headers; that is the whole problem.

## Consequences

The artlight wash works on every source, the library no longer breaks when a CDN
lacks CORS, and the client stops talking to third-party CDNs at all. The proxy
is stateless, so slice 1 adds no storage and no new failure domain.

Three honest limits:

1. **The signing key is process-scoped.** A proxy URL does not survive a
   restart — acceptable, because a client re-fetches the screen (which re-signs)
   rather than holding URLs for long. A durable cache (slice 2) sidesteps this
   for library content.
2. **Candidate selection is slice 2.** Slice 1 proxies the single poster the
   module already returns; picking the best of several candidates lands with the
   durable cache and a small SDK addition.
3. **Genuinely-missing art still falls to the client placeholder.** The proxy
   makes a *reachable* image load; a poster the source does not have (a CDN 404)
   still resolves to the SDUI typed placeholder, which is correct.

## Implementation implications

Slice 1: `internal/transport/artwork` (the signer, the SSRF-guarded client, the
handler), the `/artwork` route on the API server, and the emit-side rewriting
poster/backdrop URLs through the signer. Verified live — metahub posters that
were CORS-blocked load same-origin through the proxy on cards and detail headers
alike. Slice 2 adds the filesystem blob cache at materialise time, the candidate
list on the SDK metadata types, and the selection.
