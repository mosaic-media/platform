-- Migration 0010 — Configuration versioning and activation (MEG-015 §08).
-- Additive (expand strategy, MEG-005 §21): extends config_versions from
-- migration 0004 with the activation state machine (Draft -> Validated ->
-- Active -> Superseded, with Validated -> Rejected on failed validation).
-- config_activations and config_validation_results (also from 0004) remain
-- dormant audit-history tables for a later diagnostics slice; this
-- migration tracks current state on config_versions itself.

ALTER TABLE config_versions
    ADD COLUMN IF NOT EXISTS status            text        NOT NULL DEFAULT 'draft',
    ADD COLUMN IF NOT EXISTS validated_at       timestamptz,
    ADD COLUMN IF NOT EXISTS validation_detail  text        NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS rejected_at        timestamptz,
    ADD COLUMN IF NOT EXISTS superseded_at      timestamptz;

ALTER TABLE config_versions
    ADD CONSTRAINT config_versions_status_check
    CHECK (status IN ('draft', 'validated', 'active', 'rejected', 'superseded'));

-- At most one config version may be Active at a time. This is the
-- structural backstop behind the config.Manager activation flow, which
-- supersedes the previous Active version in the same transaction before
-- marking a new one Active.
CREATE UNIQUE INDEX IF NOT EXISTS config_versions_single_active_idx
    ON config_versions (status)
    WHERE status = 'active';
