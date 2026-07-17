-- Migration 0002 — Sessions (MEG-015 §05, First Schema Areas: Sessions).
-- Tables: sessions, remote sign-in challenges, revoked sessions.
-- Columns of `sessions` match MEG-015 §07's session table exactly.

CREATE TABLE IF NOT EXISTS sessions (
    id            text        PRIMARY KEY,
    user_id       text        NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    device_id     text        NOT NULL,
    issued_at     timestamptz NOT NULL,
    last_seen_at  timestamptz NOT NULL,
    expires_at    timestamptz NOT NULL,
    auth_strength text        NOT NULL,
    capabilities  text[]      NOT NULL DEFAULT '{}',
    revoked_at    timestamptz
);

CREATE INDEX IF NOT EXISTS sessions_user_id_idx ON sessions (user_id);

-- Remote sign-in (TV / shared-screen) challenge records, MEG-015 §07 and
-- MEG-009 §03 — Remote Device Sign-In. Table only this slice; the pairing
-- flow lands later.
CREATE TABLE IF NOT EXISTS remote_sign_in_challenges (
    id               text        PRIMARY KEY,
    device_id        text        NOT NULL,
    short_code       text        NOT NULL,
    created_at       timestamptz NOT NULL,
    expires_at       timestamptz NOT NULL,
    approved_at      timestamptz,
    approved_user_id text        REFERENCES users (id) ON DELETE SET NULL,
    consumed_at      timestamptz
);

-- Revocation audit log. `sessions.revoked_at` remains the authoritative
-- signal for Session.Revoked(); this table records the revocation event so
-- remote sign-out is observable (MEG-015 §07 — "revoke server-side session
-- records, not rely on clients deleting tokens").
CREATE TABLE IF NOT EXISTS revoked_sessions (
    session_id text        PRIMARY KEY REFERENCES sessions (id) ON DELETE CASCADE,
    revoked_at timestamptz NOT NULL,
    reason     text        NOT NULL DEFAULT ''
);
