-- The time-based second factor (ADR 0132).
--
-- It sits beside password_credentials rather than inside it because a factor is
-- not a credential: a user may have a password and no TOTP, and the row's
-- absence is the ordinary case rather than a null column.
--
-- `secret` is the one piece of credential material the Platform cannot hash — a
-- code is computed from it, so it must be recoverable. That makes this row
-- strictly more sensitive than password_credentials.hash, which is one-way.
--
-- `confirmed_at` being null means enrolment began and was never proved. Such a
-- row must never gate a sign-in: storing a factor as active on generation locks
-- out anyone whose scan silently failed.
--
-- `last_used_step` is the RFC 6238 counter of the most recently accepted code.
-- It is what makes a replay refusable — a code is valid for its whole period,
-- so without it the same six digits work twice.
CREATE TABLE IF NOT EXISTS totp_credentials (
    user_id        text        PRIMARY KEY REFERENCES users (id) ON DELETE CASCADE,
    secret         text        NOT NULL,
    confirmed_at   timestamptz,
    last_used_step bigint      NOT NULL DEFAULT 0,
    created_at     timestamptz NOT NULL
);
