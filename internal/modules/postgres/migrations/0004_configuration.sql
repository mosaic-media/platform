-- Migration 0004 — Configuration (MEG-015 §05, First Schema Areas: Configuration).
-- Tables: config versions, config activations, config validation results.
-- Activation and validation are separate from persistence (MEG-015 §08);
-- their tables exist now, but the activation/validation flow lands later.

CREATE TABLE IF NOT EXISTS config_versions (
    id           text        PRIMARY KEY,
    payload      bytea       NOT NULL,
    created_at   timestamptz NOT NULL,
    activated_at timestamptz
);

CREATE TABLE IF NOT EXISTS config_activations (
    id                text        PRIMARY KEY,
    config_version_id text        NOT NULL REFERENCES config_versions (id) ON DELETE CASCADE,
    activated_at      timestamptz NOT NULL,
    activated_by      text
);

CREATE INDEX IF NOT EXISTS config_activations_version_idx
    ON config_activations (config_version_id);

CREATE TABLE IF NOT EXISTS config_validation_results (
    id                text        PRIMARY KEY,
    config_version_id text        NOT NULL REFERENCES config_versions (id) ON DELETE CASCADE,
    valid             boolean     NOT NULL,
    detail            text        NOT NULL DEFAULT '',
    validated_at      timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS config_validation_results_version_idx
    ON config_validation_results (config_version_id);
