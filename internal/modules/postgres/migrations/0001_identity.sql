-- Migration 0001 — Identity (MEG-015 §05, First Schema Areas: Identity).
-- Tables: users, credentials (password), passkey credentials, recovery factors.
-- Expand-only: additive create statements, no destructive changes.

CREATE TABLE IF NOT EXISTS users (
    id           text        PRIMARY KEY,
    username     text        NOT NULL UNIQUE,
    email        text        NOT NULL,
    display_name text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL,
    updated_at   timestamptz NOT NULL
);

-- §05 "credentials": the local password verifier record. One per user; the
-- stored value is an opaque hash (Argon2id in production, MEG-009 §03) — the
-- Platform never persists a plaintext password.
CREATE TABLE IF NOT EXISTS password_credentials (
    user_id    text        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    hash       text        NOT NULL,
    updated_at timestamptz NOT NULL
);

CREATE TABLE IF NOT EXISTS passkey_credentials (
    credential_id text        PRIMARY KEY,
    user_id       text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    public_key    bytea       NOT NULL,
    created_at    timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS passkey_credentials_user_id_idx
    ON passkey_credentials (user_id);

CREATE TABLE IF NOT EXISTS recovery_factors (
    user_id     text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    code_hash   text        NOT NULL,
    created_at  timestamptz NOT NULL,
    consumed_at timestamptz,
    PRIMARY KEY (user_id, code_hash)
);
