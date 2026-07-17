-- Migration 0011 — Admin-managed user status (MEG-015 §09 — Users: "user
-- list, user detail and admin-managed status"). Additive (expand strategy,
-- MEG-005 §21): adds a status column to the users table from migration
-- 0001.

ALTER TABLE users
    ADD COLUMN IF NOT EXISTS status text NOT NULL DEFAULT 'active';

ALTER TABLE users
    ADD CONSTRAINT users_status_check
    CHECK (status IN ('active', 'suspended'));
