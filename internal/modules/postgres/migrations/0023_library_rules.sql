-- Migration 0023 — Library rules (platform#60).
--
-- A durable, administrator-owned statement of what the library should contain.
-- Before this, nothing anywhere said it: the library was whatever individuals
-- pressed Add on, so nothing could notice when a catalog gained a title.
--
-- Two things about this table are deliberate and would look like omissions.
--
-- **`module_id` is plain text with no foreign key.** An extension module is
-- installed and removed at runtime (platform#51) and is not a row anywhere the
-- Platform could reference — and even if it were, a rule must survive its
-- module being uninstalled: degraded and visibly so, never deleted. A cascade
-- here would silently unmake a household's decision the moment somebody
-- removed an addon.
--
-- **The last run is columns on the rule, not a history table.** What a rule
-- last did is what the administrator managing it needs to see; the full
-- per-attempt history already exists in the jobs tables, keyed by the run that
-- wrote it. A second history here would be a second answer to "what happened",
-- with its own retention question and nothing reading it.

CREATE TABLE IF NOT EXISTS library_rules (
    id           text        PRIMARY KEY,
    name         text        NOT NULL,
    -- CHECK-constrained because Platform code branches on it to decide which
    -- provider role answers the rule (platform#11's test for a closed
    -- vocabulary). A third kind is a decision, not a row.
    kind         text        NOT NULL CHECK (kind IN ('collection', 'query')),
    module_id    text        NOT NULL,
    catalog_id   text        NOT NULL DEFAULT '',
    native_type  text        NOT NULL DEFAULT '',
    query_text   text        NOT NULL DEFAULT '',
    -- Canonical form, as the nodes table stores it, so a rule narrowed by media
    -- type matches the rows it is about.
    media_type   text        NOT NULL DEFAULT '',
    -- 0 means "the Platform's default bound". Stored rather than resolved on
    -- write so that changing the default changes what unbounded rules do,
    -- instead of freezing today's number into every row ever written.
    bound        integer     NOT NULL DEFAULT 0,
    enabled      boolean     NOT NULL DEFAULT true,
    created_by   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL,

    -- The account of the last evaluation. last_run_at NULL means never run,
    -- which a surface must say rather than rendering four zeroes as though the
    -- rule had run and found nothing.
    last_run_at         timestamptz,
    last_run_matched    integer NOT NULL DEFAULT 0,
    last_run_created    integer NOT NULL DEFAULT 0,
    last_run_refreshed  integer NOT NULL DEFAULT 0,
    last_run_skipped    integer NOT NULL DEFAULT 0,
    last_run_failed     integer NOT NULL DEFAULT 0,
    last_run_error      text    NOT NULL DEFAULT ''
);

-- A rule's name is how it is identified in a run log and in the list an
-- administrator manages, so two rules cannot share one. Case-insensitively:
-- "Trending" and "trending" are the same name to the person reading them.
CREATE UNIQUE INDEX IF NOT EXISTS library_rules_name_key
    ON library_rules (lower(name));

-- The maintenance job's own read: the enabled rules, oldest first.
CREATE INDEX IF NOT EXISTS library_rules_enabled_idx
    ON library_rules (enabled, created_at);
