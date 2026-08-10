# Test Harness as development modules

**Status:** Accepted
**Date:** 2026-07-14

## Context

Module development needs deterministic Metadata, Media, Artwork, Authentication, Search, Recommendations and Event Sources without requiring every production integration.

A special mocking framework or a test-mode Platform would exercise different architecture from production, and would allow capability registration, routing, permissions and Event Bus behaviour to drift. The Developer Platform already supports statically composing development-only Modules alongside Local Modules, so the capability exists without inventing a parallel mechanism.

## Decision

Test Harness functionality is implemented through ordinary development-only Modules. Test Harness Modules provide deterministic capability implementations through normal SDK contracts, and they participate in manifest discovery, Build Pipeline composition, SDK registration, Module lifecycle, Capability Manager routing, permissions and Event Bus delivery.

The Development Supervisor should include the default Test Harness composition automatically for development workflows, and Test Harness Modules and Local Modules remain peers inside the real Development Platform. Event simulation must use declared Test Harness event-source Modules and the normal Event Bus path, so test tooling must not impersonate another Module's identity or event namespace.

## Alternatives considered

**A mock Platform.** *Rejected:* it would not exercise production Platform composition and behaviour.

**Platform test-mode branches.** *Rejected:* hidden test behaviour would weaken production fidelity.

**An external mocking framework as the primary integration environment.** *Rejected:* capability providers can supply deterministic behaviour through normal Module contracts.

**Hardcoded fixtures inside the Platform.** *Rejected:* fixture ownership belongs to replaceable Test Harness Modules.

**Arbitrary event namespace impersonation.** *Rejected:* it violates Module event ownership and weakens diagnostic identity.

**Versioned Scenario Profiles.** *Deferred:* the concept is valuable, but schema, compatibility and CLI contracts require a later protocol decision.

## Consequences

Development continuously validates the Module architecture itself, and deterministic data can support Module, UI and automated integration development without mutable external services. The Platform remains free of test-only capability implementations, which is why Test Harness packages require controls that exclude them from production composition by default. Event simulation likewise remains faithful to event ownership rather than becoming a privileged bypass.

## Implementation implications

The Test Harness should provide deterministic baseline datasets and capability providers, and the CLI may expose event and scenario controls only through declared Test Harness contracts.

The Module manifest contract should define development-only Module eligibility before that becomes stable manifest metadata, and a future Scenario Profile protocol should define versioned datasets, provider state, personas, deterministic failures and event schedules.
