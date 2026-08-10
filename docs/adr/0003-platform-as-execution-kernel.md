# Platform as execution kernel

**Status:** Accepted
**Date:** 2026-07-14

## Context

Mosaic is a self-hosted media centre whose architecture is evolving toward an operating-system-like model. The Supervisor owns installation, lifecycle, update, rollback, recovery and activation. Modules provide most business functionality. The Platform sits between them, and it must provide a stable runtime for independently developed capabilities without becoming the place where every feature lives.

Calling the Platform the centre of Mosaic risks implying that product behaviour should move into it. That would make Mosaic harder to extend, and it would cause the Platform to evolve as quickly as product features do — which is exactly the property a runtime cannot afford.

## Decision

The Platform is the execution kernel of Mosaic. It is a runtime, not an application.

It owns stable contracts, Capability Managers, orchestration, provider routing policy, the Event Bus, GraphQL assembly, Runtime SDUI production, permissions, storage, the scheduler, configuration, the Service Registry and the Runtime lifecycle.

It does not own business-specific functionality: playback, metadata interpretation, Jellyfin integration, AniList integration, TMDB integration, recommendations, search providers, artwork providers or transcoding all sit outside it.

The boundaries follow from that. The Supervisor owns lifecycle and composition around the Platform. Modules own business implementations. The SDK defines the public contract surface used by Modules. Capabilities belong to the Platform; providers belong to Modules. A new capability therefore requires Platform and SDK evolution, whereas a new provider for an existing capability should usually require only a new Module.

The final runtime is a single statically compiled Go executable assembled through the Supervisor-orchestrated Build Pipeline.

## Alternatives considered

**Platform as application.** *Rejected:* it encourages business behaviour to accumulate in the Platform, which is the failure mode this decision exists to prevent.

**Platform as feature host only.** *Rejected:* it under-specifies contracts, orchestration and lifecycle, leaving the responsibilities that must be central without an owner.

**Modules coordinate directly with one another.** *Rejected:* it creates dependency coupling between Modules and weakens Platform policy, which can no longer be enforced at a coordination point that is being bypassed.

**Dynamic runtime scanning** to discover capabilities. *Deferred:* the current architecture favours static Go composition through the Build Pipeline.

**Platform owns presentation technology.** *Rejected:* clients should render Runtime SDUI using their own presentation systems rather than receiving technology-specific instructions.

**Modules define new capability contracts independently.** *Rejected:* it fragments SDK stability and makes orchestration policy unenforceable, because the Platform can only orchestrate contracts it knows about.

## Consequences

The Platform should evolve slowly, and most user-facing functionality should evolve through Modules instead. Capability Managers become the primary orchestration point for providers, and Modules communicate through Platform contracts, Capability Managers and the Event Bus rather than through direct references to each other.

Provider metadata becomes part of the contract between Module manifests, SDK registration and Capability Managers. Orchestration policy belongs to the Platform, not to Modules.

Because the Platform owns runtime behaviour rather than domain behaviour, it can remain testable and deterministic. Client presentation stays separate for the same reason: the Platform emits Runtime SDUI rather than HTML, CSS, Flutter widgets or any other technology-specific presentation instruction.
