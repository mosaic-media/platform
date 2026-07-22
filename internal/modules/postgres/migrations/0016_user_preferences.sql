-- Migration 0016 — User preferences.
--
-- Per-user settings a user chooses for themselves: expert mode (ADR 0058) is
-- the first, and it will not be the last — theme, locale, a default screen, and
-- whatever else a user reasonably expects to persist.
--
-- Three things this deliberately is not:
--
--   * **Not the platform Config system.** That is *operator* configuration,
--     versioned and carrying reload classes (ADR 0011). A user's own choices
--     have no reload class, need no draft/validate/activate cycle, and change
--     freely at runtime.
--   * **Not module settings.** Those are one document per module, shared by the
--     whole install (ADR 0021). These are per person.
--   * **Not authorisation.** A preference reveals a surface; it never grants
--     access to anything. Expert mode is the worked example — the toggle shows
--     the diagnostics screens, and `telemetry.read` is what actually permits
--     the data behind them. Conflating the two is how a debug switch becomes a
--     disclosure.
--
-- One row per (user, key) rather than one document per user. A document would
-- match the module_settings precedent and match the wrong thing: setting one
-- preference would become read-modify-write over the rest, which races when two
-- devices write at once, and "which users have expert mode on" would need a
-- jsonb scan instead of an index.

CREATE TABLE IF NOT EXISTS user_preferences (
    user_id    text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- A dotted key, matching the config schema's shape: "ui.expert_mode".
    key        text        NOT NULL,
    -- The value as JSON, so a preference can be a boolean, a string or a small
    -- object without a migration per preference. Unvalidated by design, as
    -- ADR 0013 has it for attribute payloads: the Platform stores it, and the
    -- surface that reads it owns its meaning.
    value      jsonb       NOT NULL DEFAULT 'null',
    updated_at timestamptz NOT NULL,
    PRIMARY KEY (user_id, key)
);

-- ON DELETE CASCADE above, not RESTRICT: a preference is not a record of
-- anything that happened. When a user is removed there is nothing to preserve
-- and nothing that should block the removal — unlike content, where ADR 0013
-- makes deletion explicit and restricted.

-- "Which users have this preference set, and to what" — the query an
-- administrator surface asks, and the one a jsonb document could not serve.
CREATE INDEX IF NOT EXISTS user_preferences_key_idx
    ON user_preferences (key);
