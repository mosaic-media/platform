-- The upgrade request, and the two issue types that surround it (platform#77).
--
-- **The Platform records what somebody asked for; the Supervisor carries it
-- out.** The Platform cannot stop and restart itself onto a different
-- Generation, so an upgrade is the one thing on the register whose remedy lives
-- in the other process. The request is what crosses: it is written here, read
-- over the private handoff listener, and settled by comparing the Generation
-- the Platform is running against the one that was asked for.

-- Two new types. The set stays closed and CHECK-constrained for platform#11's
-- reason — Platform code branches on it to choose suggestions — so widening it
-- is a migration rather than an insert.
ALTER TABLE operational_issues DROP CONSTRAINT IF EXISTS operational_issues_type_check;
ALTER TABLE operational_issues ADD CONSTRAINT operational_issues_type_check
    CHECK (type IN (
        'extension_unavailable',
        'child_unrecoverable',
        'generation_rolled_back',
        'provision_failed',
        'upgrade_available',
        'upgrade_failed'));

CREATE TABLE IF NOT EXISTS upgrade_requests (
    id           text        PRIMARY KEY,
    -- The version asked for, always named and never "latest". The Platform does
    -- not hold the release catalogue and must not guess at what the newest
    -- release is, so it asks for the version it was offered — and the Supervisor
    -- resolves that name against the *signed* catalogue, so a request can never
    -- point an install at bytes nobody signed for.
    version      text        NOT NULL,
    requested_at timestamptz NOT NULL DEFAULT now(),
    -- Who asked. Kept because an upgrade is the most consequential thing a
    -- non-destructive control does, and "who pressed this" is the first question
    -- afterwards.
    requested_by text        NOT NULL DEFAULT '',
    -- Settled when the install is running the version, or when the request was
    -- abandoned. NULL means pending, which is what the handoff reports.
    settled_at   timestamptz
);

-- **At most one pending request at a time.** Two would be applied by one
-- Supervisor in an order nobody chose, which is the same reasoning that made
-- the pending *configuration* version unique in 0028. A partial index rather
-- than a column constraint, because settled rows are history and there may be
-- as many of those as there have been upgrades.
CREATE UNIQUE INDEX IF NOT EXISTS upgrade_requests_one_pending
    ON upgrade_requests ((settled_at IS NULL))
    WHERE settled_at IS NULL;
