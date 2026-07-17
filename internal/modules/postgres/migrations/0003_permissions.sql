-- Migration 0003 — Permissions (MEG-015 §05, First Schema Areas: Permissions).
-- Tables: roles, grants, resource attributes, policy audit records.

CREATE TABLE IF NOT EXISTS roles (
    id          text   PRIMARY KEY,
    name        text   NOT NULL UNIQUE,
    permissions text[] NOT NULL DEFAULT '{}'
);

CREATE TABLE IF NOT EXISTS grants (
    user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role_id text NOT NULL REFERENCES roles (id) ON DELETE CASCADE,
    PRIMARY KEY (user_id, role_id)
);

CREATE INDEX IF NOT EXISTS grants_user_id_idx ON grants (user_id);

-- ABAC subject attributes (MEG-009 §04 — Attribute-Based Access Control).
-- Keyed per subject so PermissionStore.AttributesForUser can resolve them.
CREATE TABLE IF NOT EXISTS resource_attributes (
    subject_user_id text NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    key             text NOT NULL,
    value           text NOT NULL,
    PRIMARY KEY (subject_user_id, key)
);

-- Explainable, auditable policy decisions (MEG-009 §04 — Auditability).
-- Table only this slice; the policy engine does not yet persist here.
CREATE TABLE IF NOT EXISTS policy_audit_records (
    id              text        PRIMARY KEY,
    subject_user_id text,
    action          text        NOT NULL,
    resource_type   text        NOT NULL DEFAULT '',
    resource_id     text        NOT NULL DEFAULT '',
    allowed         boolean     NOT NULL,
    reason          text        NOT NULL DEFAULT '',
    decided_at      timestamptz NOT NULL
);
