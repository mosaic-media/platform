# Developer Platform as an integrated toolchain

**Status:** Accepted
**Date:** 2026-07-14

## Context

The Mosaic SDK defines the public contract between Platform and Modules, but a library alone does not provide scaffolding, local composition, real-Platform integration testing, validation, packaging or publication workflows.

Requiring Module authors to understand Supervisor internals, Build Workspaces, generated imports and runtime assembly would expose infrastructure that the Developer Platform can automate consistently. That development convenience must not, however, create a second Platform architecture or a runtime plugin model.

## Decision

Mosaic defines a Developer Platform composed of:

- the Mosaic SDK
- the Mosaic CLI
- the Development Supervisor
- a production-equivalent Development Platform
- Test Harness Modules
- local Module composition
- validation, packaging and publication tooling

The SDK owns developer contracts and the CLI owns developer workflow, while the Development Supervisor orchestrates local rebuilds through the Build Pipeline. It is a development-only Supervisor implementation that should reuse production Supervisor behaviour wherever practical, while adding file watching, local source mapping, automatic restart, client notification and development diagnostics.

The Development Platform is the real Platform running in development configuration, and Test Harness functionality is delivered through explicit Modules that provide deterministic capability implementations. Local Modules are statically composed into candidate Platform binaries and do not require publication to a remote catalogue, so Test Harness Modules and Local Modules participate as peer Modules in the same Development Platform composition.

## Alternatives considered

**The SDK library as the complete developer experience.** *Rejected:* a contract library should not own scaffolding, orchestration, packaging or publication workflows.

**A mock development runtime.** *Rejected:* it would allow development behaviour to drift from the real Platform.

**Runtime hot loading for local Modules.** *Rejected:* it would create a development-only plugin architecture unlike production composition.

**Platform-owned fake capabilities.** *Rejected:* Test Harness Modules preserve normal capability contracts without adding hidden test behaviour to the Platform.

**Manual Go and Build Workspace orchestration.** *Rejected:* it exposes repetitive infrastructure and creates inconsistent development environments.

**Publication required for every local rebuild.** *Rejected:* local path composition can preserve static linking without remote catalogue publication.

**A separate development composition engine.** *Rejected:* development should continuously exercise the production Build Pipeline and static composition model.

## Consequences

Module development can feel like ordinary Go while using a real Mosaic Platform, and the CLI becomes the primary interface for Mosaic-specific developer workflows. Development and production retain the same static Module composition architecture, so the Developer Platform becomes a broader ecosystem than the SDK without weakening the SDK's intentionally small contract boundary. Developer tooling must therefore remain observable enough to diagnose build, registration, health-check and activation failures.

## Implementation implications

The CLI should support a coherent path from scaffolding to publication, with conceptual commands as follows.

```text
mosaic new module
mosaic dev
mosaic build
mosaic test
mosaic doctor
mosaic validate
mosaic package
mosaic publish
mosaic docs
```

The Development Supervisor should watch local sources, request isolated rebuilds, use development health-check timing and expose verbose diagnostics. After a healthy candidate is activated, it should restart or switch the Development Platform and notify connected clients. The CLI may launch the Web Shell and browser as part of `mosaic dev`, while allowing headless operation.

The Build Pipeline should continue to own workspace preparation, Go dependency handling, generated imports and compilation. Publishing protocols and stable command schemas should be specified separately before they become compatibility commitments.
