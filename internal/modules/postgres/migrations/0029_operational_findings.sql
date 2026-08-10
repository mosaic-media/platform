-- Migration 0029 — The resolution register (platform#74).
--
-- What is wrong with this install, now. Every operational failure Mosaic has is
-- a log line that scrolls away: nothing survives a restart, nothing is addressed
-- to anybody, and nothing says what to do about it. That was tolerable while the
-- Platform only answered requests; M4 makes it untenable, because the Supervisor
-- now takes decisions on the user's behalf — restarting a child, reverting a
-- Generation whose health check failed — with nowhere to record them.
--
-- Three things here are deliberate and would each look like an omission.
--
-- **The identity is (type, context, reference), not a fresh row per detection.**
-- A module that fails to start on every boot is one situation that has been
-- happening since Tuesday, not fourteen problems. So a re-raise updates
-- last_seen and increments occurrences, and first_seen is never moved — which is
-- the number that tells somebody whether this began with the upgrade they did
-- last week.
--
-- **Suggestions are not stored.** What a build can do about a situation is a
-- property of that build, and a row written by an older one must not pin an
-- offer this one no longer honours. They are derived on read from the type.
--
-- **`reference` is plain text with no foreign key**, for the same reason the
-- library rules' module_id is: an extension is installed and removed at runtime
-- (platform#51) and is not a row anywhere to reference, a Generation belongs to the
-- Supervisor and is not in this database at all, and a finding must outlive the
-- thing it is about — that is most of its value.

CREATE TABLE IF NOT EXISTS operational_issues (
    id          text        PRIMARY KEY,
    -- CHECK-constrained because Platform code branches on it to choose which
    -- suggestions to offer, which is platform#11's test for a closed vocabulary.
    -- A fifth type is a decision, not a row.
    type        text        NOT NULL CHECK (type IN (
                    'extension_unavailable',
                    'child_unrecoverable',
                    'generation_rolled_back',
                    'provision_failed')),
    context     text        NOT NULL CHECK (context IN (
                    'extension', 'child', 'generation', 'host')),
    -- Empty for a host-level finding, which is why this is NOT NULL DEFAULT ''
    -- rather than nullable: the unique index below has to treat "no reference"
    -- as a value, and NULL is not equal to itself in one.
    reference   text        NOT NULL DEFAULT '',
    -- Which process detected it. "The Supervisor could not start the Platform"
    -- and "the Platform could not start a module" are different situations with
    -- different remedies, and a register that flattened them would make the
    -- first unreadable.
    source      text        NOT NULL CHECK (source IN ('platform', 'supervisor')),
    -- One sentence for a person. Nothing branches on it — a client that cannot
    -- render it still knows what the issue is from its type.
    detail      text        NOT NULL DEFAULT '',
    first_seen  timestamptz NOT NULL,
    last_seen   timestamptz NOT NULL,
    occurrences integer     NOT NULL DEFAULT 1
);

-- The identity of a situation. A re-raise finds this row rather than adding one.
CREATE UNIQUE INDEX IF NOT EXISTS operational_issues_identity_key
    ON operational_issues (type, context, reference);

-- The register's own read: everything open, most recently seen first, which is
-- the order somebody scanning for "what is wrong now" wants.
CREATE INDEX IF NOT EXISTS operational_issues_last_seen_idx
    ON operational_issues (last_seen DESC);
