-- Migration 0022 — the session credential becomes a bearer pair (ADR 0102).
--
-- A session was a fixed 24-hour lifetime with no renewal, and the client held
-- its id in memory. It is now a session row plus two token families: a
-- minutes-long access token presented on every call, and a long-lived refresh
-- token that is rotated on every use.
--
-- **Only hashes are stored.** A token is high-entropy random bytes, so the
-- hash is SHA-256 rather than a password KDF: there is nothing to brute-force
-- back from a 256-bit random value, and a per-request Argon2 verification on
-- the authentication path would be a self-inflicted denial of service. What the
-- hash buys is that a database read — a backup, a log, a compromised replica —
-- does not hand anybody a usable credential.

CREATE TABLE IF NOT EXISTS session_access_tokens (
    token_hash text        PRIMARY KEY,
    session_id text        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    issued_at  timestamptz NOT NULL,
    expires_at timestamptz NOT NULL
);

CREATE INDEX IF NOT EXISTS session_access_tokens_session_idx ON session_access_tokens (session_id);
-- The sweep that drops expired tokens reads this. Without it, the one query
-- that runs on a schedule forever would be a sequential scan of the table that
-- grows fastest in the schema.
CREATE INDEX IF NOT EXISTS session_access_tokens_expiry_idx ON session_access_tokens (expires_at);

CREATE TABLE IF NOT EXISTS session_refresh_tokens (
    token_hash text        PRIMARY KEY,
    session_id text        NOT NULL REFERENCES sessions (id) ON DELETE CASCADE,
    -- The chain this token belongs to: every rotation of one original
    -- credential shares it. Reuse detection revokes the *chain*, not the
    -- token, because by the time a replay is seen the attacker and the
    -- legitimate client are both holding descendants of the same original.
    chain_id   text        NOT NULL,
    -- The device the token is bound to. A refresh presented from another
    -- device is a stolen credential being used, not a client that moved.
    device_id  text        NOT NULL,
    issued_at  timestamptz NOT NULL,
    expires_at timestamptz NOT NULL,
    -- When this token was exchanged. Non-null means spent: presenting it again
    -- is the replay that revokes the chain.
    used_at    timestamptz,
    revoked_at timestamptz
);

CREATE INDEX IF NOT EXISTS session_refresh_tokens_chain_idx   ON session_refresh_tokens (chain_id);
CREATE INDEX IF NOT EXISTS session_refresh_tokens_session_idx ON session_refresh_tokens (session_id);
CREATE INDEX IF NOT EXISTS session_refresh_tokens_expiry_idx  ON session_refresh_tokens (expires_at);

-- Every session issued before this migration was a 24-hour credential a client
-- held in memory, with no token rows behind it. It cannot be refreshed and
-- nothing can present an access token for it, so it is expired here rather than
-- left to look valid to a query that does not know the difference.
UPDATE sessions SET revoked_at = now() WHERE revoked_at IS NULL;
