-- Migration 0028 — a configuration version can be waiting for an escalation.
--
-- Before this, a change that could not be applied without a restart was
-- classified correctly and then left Validated, which lost the fact that
-- somebody had asked for it. A restart could not tell a candidate that merely
-- passed validation from one a user chose, so it had to apply all of them or
-- none. 'pending' is that intent, recorded.

ALTER TABLE config_versions
    ADD COLUMN IF NOT EXISTS requested_at timestamptz;

ALTER TABLE config_versions
    DROP CONSTRAINT IF EXISTS config_versions_status_check;

ALTER TABLE config_versions
    ADD CONSTRAINT config_versions_status_check
    CHECK (status IN ('draft', 'validated', 'pending', 'active', 'rejected', 'superseded'));

-- At most one version may be waiting for an escalation, for the same reason
-- at most one may be Active: two pending changes would both be applied by one
-- restart in an order nobody chose, and the second would silently win.
CREATE UNIQUE INDEX IF NOT EXISTS config_versions_single_pending_idx
    ON config_versions (status)
    WHERE status = 'pending';
