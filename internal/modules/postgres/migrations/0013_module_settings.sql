-- Migration 0013 — Module settings (platform#17).
-- One user-managed settings document per optional module, keyed by module id.
-- Set at runtime through the configureModule command; the Platform stores it
-- opaquely and the module interprets it (platform#9's unvalidated-JSON rule
-- applied to configuration). This is deliberately NOT the versioned platform
-- Config system: that is operator configuration with reload classes, whereas
-- module settings are user-owned data that changes freely at runtime.

CREATE TABLE IF NOT EXISTS module_settings (
    module_id  text        PRIMARY KEY,
    settings   jsonb       NOT NULL DEFAULT '{}',
    updated_at timestamptz NOT NULL
);
