-- Migration 0020 — Installed extensions (platform#51).
-- The durable set of extension modules a user has installed from a trusted
-- repository. Default-empty: a fresh Platform composes only its core modules,
-- and every row here is an extension a user chose. The Platform reads this at
-- boot and re-adopts each — re-verify then spawn — and writes it on install and
-- uninstall.
--
-- It records identity and provenance, not the binary: the verified bytes on
-- disk are a cache re-acquirable from this record. This is deliberately NOT the
-- module_settings table — whether the Platform HAS a module and how a module it
-- has is CONFIGURED are different questions (platform#51).

CREATE TABLE IF NOT EXISTS installed_extensions (
    module_id    text        PRIMARY KEY,
    repository   text        NOT NULL,
    version      text        NOT NULL,
    signed_by    text        NOT NULL,
    installed_at timestamptz NOT NULL
);
