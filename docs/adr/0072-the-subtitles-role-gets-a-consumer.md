# The subtitles role gets a consumer, and the Platform fetches what it finds

**Status:** Accepted. **Built.**

**Date:** 2026-08-01

## Context

[module-stremio-addons#1](https://github.com/mosaic-media/module-stremio-addons/blob/main/docs/adr/0001-completing-the-stremio-source-surface.md) defined the `subtitles`
provider role. Two modules implement it. `SubtitlesRequest` grew `Season` and
`Episode` in SDK `v0.26.0` so a provider could answer for an episode and not only
a film. **Nothing has ever called it.**

The shape of the gap is worth stating precisely, because it is not what "unbuilt"
usually looks like. The registry could resolve a subtitles provider *by name* —
`SubtitlesProvider(id)` — and no code path anywhere knew a name to ask for. The
*plural* enumerator that every other fanned-out role has
(`StreamProviders`, `ArtworkProviders`) did not exist, because nothing had ever
needed one. So the role was fillable, filled, correctly addressable, and
unreachable: not a missing feature, a missing call.

It matters most for exactly the releases Mosaic is built around. A remote source
often has no subtitles of its own, and
[platform#68](0068-subtitles-are-a-rendition.md)'s embedded path cannot help there —
there is nothing embedded to extract.

## Decision

**Every installed subtitles provider is asked at play time, and the origin
fetches what they return.**

1. **Asked at play, not stored at import.** A subtitle URL is perishable in the
   same way a debrid link is, and resolving them into the graph at import buys
   entries that have been decaying since before anyone wanted them — the mistake
   [platform#28](0028-resolution-cache-and-capability-classes.md) already names for
   streams.

2. **Fanned out over every provider, handed shared identities.** The same shape
   stream enrichment uses and for
   [platform#46](0046-stream-resolution-is-decoupled-from-metadata-provenance.md)'s
   reason: a subtitles provider is asked about content it did not source, so it
   gets a neutral external id rather than a native one it could not have.

3. **Best-effort throughout.** Every failure logs and continues. A subtitle
   source that is down, unconfigured or simply does not know the title costs the
   extra tracks and never the playback.

4. **The origin fetches; the URL never reaches a client.** A module resolves and
   the Platform serves ([platform#25](0025-playback-consumer-and-media-origin.md)),
   and the reason is concrete rather than architectural hygiene: the URL may carry
   a credential, and pointing a browser at it also hands a third party the
   viewer's address.

5. **ffmpeg does the fetching as well as the conversion.** It already speaks
   every scheme a module might return, already carries the reconnect behaviour the
   rest of the origin needs, and a file that is already WebVTT costs a passthrough
   rather than a second code path.

6. **None is ever default.** The release's own tracks are what a viewer's
   preference was resolved against ([platform#67](0067-language-is-a-persons-preference.md));
   a file from elsewhere turning itself on would override a decision nobody asked
   it to make. The viewer picks one from the player's menu.

## Alternatives

**Resolve subtitles at import, beside stream enrichment.** *Rejected.* It is
where the code would naturally have gone, and it caches perishable URLs at the
moment they have the longest time to go stale before anyone plays.

**Hand the client the module's URL and let it fetch.** *Rejected.* Fewer moving
parts and it leaks a credential and the viewer's address to a third party. It
also breaks the moment a provider needs a header, which the origin can supply and
a `<track>` element cannot.

**Convert with a Go SubRip parser rather than ffmpeg.** *Rejected.* It is a small
library and it would be a second fetching path, a second scheme table and a second
set of reconnect behaviour, to avoid a process for a file measured in kilobytes.

## Consequences

- **This is the one subtitle path a direct-played release can have.** An embedded
  track needs a playlist to hang an HLS rendition off, so [platform#68](0068-subtitles-are-a-rendition.md)'s path is
  unavailable when nothing is transcoding. A file from elsewhere has no such
  constraint, so it is served on the relayed path too — which is why the resource
  is reachable under a direct-play ticket, the only sub-resource that is.
- **Deduped by URL.** A title carrying both an IMDb and a TMDB id would otherwise
  list every file twice, once per identity the provider was asked under. The first
  identity that answers ends the questioning for that provider, for the same
  reason.
- **The module is named in each label.** Two sources answering for one title is
  the ordinary case, and a menu with "English" twice is one a viewer cannot choose
  from.
- **`v1.Subtitle` still cannot say a track is forced**, so [platform#67](0067-language-is-a-persons-preference.md)'s forced
  behaviour remains complete for embedded tracks and unavailable for these, exactly
  as that record predicted. It is an additive SDK field whenever the publish train
  moves.
