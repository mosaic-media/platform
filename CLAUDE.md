# Claude Instructions — Mosaic Platform

## Source of truth

- The [`mosaic-architecture`](https://github.com/mosaic-media/mosaic-architecture) repository (`mosaic-media/mosaic-architecture`) is the canonical source for all Mosaic design and architecture decisions. This repository implements what's specified there — it does not redefine it.
- **MEG-015 — Platform Foundation Implementation** is the primary build guide for this repository's first implementation. Read it in full, in build order, before starting any slice.
- Required reading for Platform implementation work, per MEG-015 §00 (Document Control):
  - MAC-001 — Platform Architecture
  - MEG-001 — Go Engineering Standards
  - MEG-004 — Hexagonal Architecture
  - MEG-005 — Runtime Architecture
  - MEG-006 — Module Platform
  - MEG-007 — Storage Architecture
  - MEG-009 — Security Architecture
  - MIP-006 — Generation Composition Protocol
- Whenever a section in MEG-015 (or any other spec) references another document by ID — MEG-005, MEG-009, MIP-004, etc. — open that document in `mosaic-architecture` and read the relevant section before implementing. Never guess at its contents or substitute general engineering knowledge for what a linked spec actually says.

## Package tier model (correction to MEG-015 §02)

MEG-015 §02 — Repository Layout describes a two-tier model: `internal/platform/*` (private Platform code) and `internal/adapters/*` (with Postgres under `internal/adapters/postgres/`). Before implementation began on this repo, that layout was corrected to **three tiers**. Use this model, not MEG-015 §02's original layout:

1. **Core Platform** — `internal/platform/*`
   Domain, contracts, application services. Fully trusted, compiled in, defines the rules everything else follows.

2. **Built-in module** — `internal/modules/*`
   Infrastructure that implements Platform contracts using the same registration/manifest shape a future external Module would use (per MEG-006), but compiled in, required, and fully trusted — not sandboxed, not independently versioned, not optional.
   Postgres is the first example: it lives at `internal/modules/postgres/` (**not** `internal/adapters/postgres/`), registered through `internal/composition/builtin/` the same way an external Module would be discovered.

3. **External module** (future, per MEG-006)
   Product/domain capability packs — anime, manga, etc. Governed, independently versioned, discovered at runtime. **Not** part of this repo's initial scaffold.

`internal/adapters/` is reserved for things that are **not** module-shaped — helpers that don't implement a full contract surface on their own (filesystem utilities, crypto helpers). Do not put Postgres there.

**Outstanding:** this is a correction made before implementation began, not yet reflected in MEG-015 itself. MEG-015 §02 should be updated to match the next time a documentation sync pass runs against `mosaic-architecture`. Do not silently edit MEG-015 from an implementation session in this repo — flag it and let a docs session handle it.

## Non-negotiable rules (from MEG-015)

- **Dependency direction** (§02): dependencies point inward. Transport → Application services → Contracts/Domain. Adapters/Modules → Contracts → External systems. Domain must never import transport, adapter, or database packages. Application services may depend on Platform contracts, never on concrete Postgres (or other module) types.
- **Error categories** (§03): every contract error maps to one of `InvalidArgument`, `Unauthenticated`, `PermissionDenied`, `NotFound`, `Conflict`, `Unavailable`, `Internal`. Adapters/modules may keep driver-specific errors internally; application services and transports must only ever see these Platform categories.
- **Command handler order** (§04): every command handler follows the same sequence — validate command shape → authenticate caller → authorize via policy → open a `UnitOfWork` → load state through contracts → apply domain rules → persist state and outbox events in the same transaction → return a Platform result type.
- **GraphQL resolvers call services only** (§09): GraphQL is a transport and projection surface, not a persistence layer. Resolvers must call application command or query services — never open a database connection or query Postgres (or any module) directly.
- **Config reload classes** (§08): every configuration field declares a reload class — `Hot` (applies without restart), `Restart` (requires process restart), `Generation` (requires Supervisor to activate a new Generation), `Recovery` (applies only through recovery flow). Classify new config fields before adding them.

## Workflow

- Develop and commit directly on `main`. This repository does not use feature branches for Platform implementation work.
- Build one slice at a time, in the order defined by MEG-015 §12 — Build Sequence. Do not start a slice whose prerequisites haven't landed.
- Each slice must pass its MEG-015 §11 test gate before the next dependent slice begins.
- Run `go build ./...` and `go test ./...` before declaring any slice done.
- Commit per passing slice — one commit (or focused set of commits) per slice, not one commit for the whole build sequence.
- When ambiguity comes up, check Required Reading (and whatever spec the ambiguous area maps to) before guessing. Do not substitute assumption for a documented decision.

## Current status

Slices from MEG-015 §12 — Build Sequence:

- [x] Repository scaffold — includes creating `internal/modules/` as well as `internal/adapters/` per the tier correction above; Postgres does not belong under `internal/adapters/`.
- [x] Core contracts — first contract set (`UnitOfWork`/`Tx`, `UserStore`, `SessionStore`, `PermissionStore`, `ConfigStore`, `EventOutbox`, `EventPublisher`, `SecretBroker`, `Clock`, `IDGenerator`, `HealthProbe`) landed in `internal/platform/contracts/`, backed by Platform value types in `internal/platform/domain/`. `ErrorCategory` scheme (7 categories) and `ContractID`/`ContractVersion` metadata added. No adapters or application services written. `go build ./...` and `go test ./...` pass.
- [x] Application service skeleton — `internal/platform/app/` implements the full MEG-015 §04 command order (validate → authenticate → authorize → open `UnitOfWork` → load state → apply domain rules → persist state+outbox in one transaction → return a Platform result) via `CreateLocalUser`, and the query gate (authenticate → authorize → read) via `GetUserByID`. Policy was a stubbed `PolicyDecisionPoint` at this point (subject/action/resource/context shape, default-deny zero value) — superseded by the real engine in the next slice. No Postgres adapter; command/query flow is proven against in-memory fakes with a call-order trace and transaction rollback semantics. `go build ./...` and `go test ./...` pass.
- [x] Identity, sessions and policy — replaced the stub `authorize()` and stub user command/query with the real local-identity model. `internal/platform/domain/`: `Session` now matches MEG-015 §07's table exactly (`session_id`, `user_id`, `device_id`, `issued_at`, `last_seen_at`, `expires_at`, `auth_strength`, `capabilities`) plus `RevokedAt`; added `PasswordCredential`/`PasskeyCredential`/`RecoveryFactor` and a `PasswordVerifier` port (hash/verify only — no real crypto yet). `internal/platform/contracts/`: added `CredentialStore` and `Tx.Credentials()`. `internal/platform/policy/`: real ABAC-shaped `Engine` (`Subject`/`Action`/`Resource`/`PolicyContext` → `Decision`) doing RBAC lookups against `PermissionStore`, default-deny, with its own unit tests. `internal/platform/sessions/`: `Manager` with `Issue`/`Validate`/`Revoke` against a `SessionStore` parameter, with its own unit tests. `internal/platform/app/`: `Service` now enforces every policy decision (the enforcement point, per §07) and gained `AuthenticateLocalUser` (password login → session issuance) and `RevokeSession`; `CreateLocalUser` now also persists a password credential in the same transaction. Audit events (`authentication.succeeded/failed`, `authorization.denied`, `session.revoked`, `user.created`) go through the `EventOutbox`/`EventPublisher` fakes. Still no Postgres adapter — everything runs against in-memory fakes with a real `policy.Engine` (not a hardcoded allow/deny) driving every decision. Tests prove: a user can be created and then authenticate; a session can be issued, validated, and revoked, and a revoked session fails validation; and a policy-denied action (`CreateLocalUser` and `RevokeSession`) is rejected without mutating any state (MEG-015 §11 Policy gate). `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass (a C compiler was installed to enable `-race`).
- [x] PostgreSQL adapter and migrations — built the mandatory first storage adapter as the built-in module `internal/modules/postgres/` (NOT `internal/adapters/`, per the tier correction), using `pgx/v5`. Embedded SQL migrations (`migrations/*.sql`) create every table in MEG-015 §05's First Schema Areas (identity, sessions, permissions, configuration, events, jobs, diagnostics, storage registry); a deterministic in-house migrator (`migrate.go`) tracks applied versions + checksums in `platform_schema_migrations`, applies each pending migration with its tracking row in one transaction, and **fails fast** (fail-fast per MEG-007 §10) on a missing, incompatible (checksum mismatch), partially-applied (version gap), or database-ahead (unknown version) schema. Postgres implementations of every contract (`UnitOfWork`+`Tx`, User/Session/Permission/Config/EventOutbox/Credential stores, Clock, IDGenerator, HealthProbe); `UnitOfWork.WithinTx` shares one `pgx.Tx` across all `Tx` stores so state + outbox commit atomically by construction. `errors.go` maps pgx/SQLSTATE to the seven Platform categories (23505/40001/40P01 → Conflict, etc.); no pgx row, SQLSTATE, or driver type escapes the module boundary. Module declares a manifest via `internal/composition/builtin/` (registry mirrors external-Module discovery, no import cycle). `main.go` now registers the module and runs the startup migration gate when `MOSAIC_POSTGRES_DSN` is set. Reusable, adapter-agnostic contract suite in `test/contract/` runs against a **real** PostgreSQL (embedded-postgres by default, or `MOSAIC_TEST_POSTGRES_DSN` for docker-compose — added `docker-compose.yml` + README). Tests prove: migrations run from empty, idempotency, all four fail-fast detections, outbox/state atomicity + rollback, concurrent-unique → exactly one Conflict, and the **app services from the previous slices run unchanged against the real adapter** (adapter-agnostic contracts confirmed — no edits to `internal/platform/app`). `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass with the real database.
- [x] Transactional outbox — made the state+event atomicity guarantee structural and enforced, and implemented the full event envelope + delivery failure bookkeeping (MEG-015 §06). Atomicity was already structural (`UnitOfWork.WithinTx` shares one `pgx.Tx` across every `Tx` store, single `Commit`/deferred `Rollback`); this slice adds the definitive proof — `TestOutboxStateAtomicOnMidTransactionFailure` injects an error AFTER the user write and outbox append but BEFORE commit, then queries the raw tables directly (bypassing the stores) to confirm NEITHER row persists. `domain.Event` now carries the full §06 envelope (`event_id`/`event_type`/`occurred_at`/`recorded_at`/`actor`/`tenant_scope`/`correlation_id`/`causation_id`/`payload`/`redaction_class`, with a `RedactionClass` enum — first-cut, redact-by-default, since no taxonomy is specced yet). `domain.OutboxEvent` gained the §06 Failure-Behaviour fields (`Attempts`, `LastErrorCategory`, `NextRetryAt`, `DeadLettered`, `OwningComponent`), and `domain.DeliveryPolicy` holds the pure retry/dead-letter rule (exponential backoff, dead-letter at max attempts) with its own unit tests. Additive migration `0009_event_envelope.sql` (expand strategy — did NOT edit 0005, which would break the checksum guard) adds the envelope + failure columns. `contracts.EventOutbox` gained `RecordFailure` (increments attempts, applies the policy, schedules retry or dead-letters); the postgres outbox writes/reads the full envelope, excludes dead-lettered rows from `ListUnpublished`, and defaults unclassified payloads to redacted. The app service now stamps `actor`/`recorded_at`/`redaction_class` on emitted events via a shared `newEvent` helper. **No worker, publishing, Event Bus, or subscriber built — that is the next slice.** `go build ./...`, `go vet ./...`, and `go test ./... -race` all pass against real PostgreSQL (embedded + dockerized PG 16).
- [ ] In-process Event Bus
- [ ] Configuration and secret broker
- [ ] GraphQL command and query surface
- [ ] Diagnostics and health
- [ ] Supervisor handoff
- [ ] Reference capability path
- [ ] SDK extraction readiness
