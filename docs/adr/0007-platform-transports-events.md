# Platform transports events, modules own domain events

**Status:** Accepted
**Date:** 2026-07-14

## Context

Mosaic needs event-driven communication between independently developed Modules.

A traditional typed event bus would require the SDK or the Platform to define every possible event type, which would make the SDK change whenever a Module adds or evolves a domain event. It would also make the Platform aware of domain concepts that should remain Module-owned.

## Decision

The Platform owns generic event infrastructure. That means the Platform owns:

- the Event Bus
- the Event Envelope
- routing
- subscription
- delivery
- reliability
- observability

The SDK owns:

- the Event Envelope contract
- Event Bus interfaces
- core Platform lifecycle events

Modules own domain events, which means Modules own:

- event names
- payload definitions
- semantics
- documentation
- versioning
- publishers
- subscribers

Event names must be namespaced. Module events may be public or private: public Module events form the Module's documented integration contract, whereas private Module events remain implementation details.

## Alternatives considered

**The SDK owns every event type.** *Rejected:* the SDK would change every time a Module added or evolved a domain event, making the most stable repository in the system the most frequently edited.

**The Platform interprets event payloads.** *Rejected:* it couples Platform infrastructure to business domains.

**Flat, un-namespaced event names.** *Rejected:* it risks collisions and weakens diagnostics.

**All Module events are public.** *Rejected:* it prevents Modules from evolving internal implementation events freely.

**Direct Module communication.** *Rejected:* it breaks loose coupling and Module isolation.

## Consequences

The SDK remains stable while the event ecosystem grows, because Modules can evolve domain events independently and the Platform can route, deliver, observe and replay events without understanding payload semantics.

Public and private event visibility gives Module authors an explicit external API boundary, and manifests become the discovery and validation surface for published and subscribed events.

## Implementation implications

The event protocol and the manifest declaration of public and private event publications and subscriptions are defined separately from this decision. The Event Bus should route by namespaced event name and honour visibility metadata, while tooling should generate event documentation from manifests. Subscribers should depend on public Module events or Platform events, never on another Module's private events.
