# The live session over a bidirectional WebSocket

**Status:** Superseded by [contracts#5](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0005-cross-client-transport-two-lane-rpc.md)
— its goals (streaming input, server push, region updates, a
technology-agnostic client) are unchanged and carried forward; only the wire
changed, from a bespoke WebSocket to a two-lane Connect/gRPC surface.
**Date:** 2026-07-20

## Context

The SDUI emit-side ([platform#19](0019-sdui-emit-side.md)) serves screens over HTTP
request/response: the client asks for a screen, the server answers, done. That
shape has three limits for a live application, and it is worth being precise
about which matter:

- **Latency** — *weak* justification here. Mosaic is self-hosted; a localhost
  round trip is ~1 ms, so a persistent connection barely helps raw latency. This
  is not the reason.
- **Streaming input (search-as-you-type)** — *strong*. Results should populate
  as a user types, not on Enter. That wants a low-latency, always-open channel
  the client streams keystrokes down and the server streams results back into a
  region.
- **Server push** — *strong, and impossible with request/response*. A scan
  finishing, a download completing, an admin broadcast, a library change from
  another device — the server must reach the client unprompted. HTTP cannot.

So the case for a live channel rests on **streaming input** and **server push**,
not latency.

## Decision

**The client and Platform hold one persistent, authenticated, bidirectional
WebSocket — the live session. The client sends *intents*; the server pushes
*UI updates* into named regions. Request/response screen fetching is superseded
as the primary path.**

- **Client → server: intents.** `navigate(screen, params)`, `input(region,
  value)` (a keystroke in a field), `invoke(mutation, input)` (an action),
  `subscribe/unsubscribe`. An intent is a thin envelope; it resolves to the same
  application command or query the HTTP transport would have — the WebSocket is a
  transport, not a second application layer, and the command boundary
  ([platform#12](0012-published-contract-surface.md)) is unchanged.
- **Server → client: UI updates.** Region-targeted render operations —
  *replace / append / patch* the `UINode`s of a named region — plus toasts,
  banners and shell mutations. This is the **region-update protocol
  [platform#19](0019-sdui-emit-side.md) deferred** (its `Into` targeting): the
  server says "put this tree in the results region," "append this toast." The
  app shell ([platform#21](0021-server-owned-app-shell.md)) is the initial push;
  navigation and search are subsequent region updates, not new pages.
- **Search-as-you-type debounces server-side.** The client streams input events;
  the Platform **coalesces** them (a short debounce window) before sourcing, so a
  fast typist does not fan out a request per keystroke to Cinemeta/Torrentio and
  trip their rate limits. The client may debounce too, but the Platform is the
  backstop that protects the upstreams.
- **The session is stateful, and the Platform owns resilience.** A live session
  is per-connection state (the current shell, open subscriptions, the caller).
  The Platform handles **reconnection** (the client re-attaches and the server
  rebuilds or resumes the session), ordering, and backpressure. Authentication
  happens on connect (the session `Caller`, [platform#13](0013-how-a-capability-acts.md)),
  once, rather than per message.

The transport stays technology-agnostic ([contracts#1](https://github.com/mosaic-media/contracts/blob/main/docs/adr/0001-server-driven-ui-and-the-shell.md)):
a WebSocket plus a versioned message protocol is universal, so a Flutter client
speaks the same session. A one-shot HTTP path (the `screen` query) may remain as
a fallback and for tools, but the Shell's normal life is the socket.

## Alternatives considered

**Keep HTTP request/response (status quo).** *Rejected:* no server push at all,
and search-as-you-type becomes a request per keystroke. Not a live app.

**Hybrid: HTTP intents + SSE for push.** *Considered.* Server-Sent Events give
server→client push simply, keeping writes on plain HTTP. But SSE is one-way;
search-as-you-type wants low-latency client→server streaming too, which SSE
cannot carry, so it would still need a second upward channel. One bidirectional
socket is cleaner for a genuinely live session. (For a push-only future need,
SSE would have sufficed; this decision is driven by streaming input.)

**GraphQL subscriptions (graphql-ws).** *Considered.* It reuses the schema, but
the live protocol here is region-update and shell-mutation operations, which are
not a natural fit for subscription-result semantics. A purpose-built message
protocol over the socket is more direct; whether it is carried *inside*
graphql-ws framing is an implementation detail, not an architectural one.

**Stateless "long-poll" screens.** *Rejected:* it reintroduces request/response
overhead and cannot push, solving neither real problem.

## Consequences

The client becomes live: input streams up, results and alerts stream down, and
the server drives the whole session over one channel. The region-update protocol
lands — forced by this work — which also completes what [platform#19](0019-sdui-emit-side.md)
left open and what the server-owned app shell ([platform#21](0021-server-owned-app-shell.md))
needs to fill its content region.

Honest costs, all acceptable for a self-hosted server but real:

1. **The server is now stateful.** Live sessions are per-connection state, unlike
   the stateless HTTP surface. Horizontal scaling would need sticky sessions or
   shared session state; single-node self-hosting does not, but the design must
   not assume one node forever.
2. **Resilience is now the Platform's job.** Reconnection, message ordering,
   backpressure and idempotent replay of an interrupted intent are real
   engineering the request/response model got for free from HTTP.
3. **The protocol must be versioned.** A long-lived socket outlives a deploy; the
   message schema needs explicit versioning and graceful handling of a client a
   version behind.

## Implementation implications

A new WebSocket transport surface on the Platform (alongside the GraphQL HTTP
API), a session manager holding per-connection live state, the intent → command
routing (reusing the application services unchanged), and the region-update
message vocabulary. The Shell ([platform#21](0021-server-owned-app-shell.md))
connects on boot, renders the pushed app shell, streams intents, and applies
region updates — falling back to its standby state when the socket drops.
Sequenced after the server-owned app shell so the chrome model is settled before
the transport under it changes.
