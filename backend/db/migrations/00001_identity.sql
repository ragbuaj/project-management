-- +goose Up

-- Bounds every migration, and the house convention for all of them.
--
-- Without lock_timeout a migration queues behind whatever long query happens
-- to be running, and everything arriving after it queues behind the migration.
-- One slow report then stalls the whole database. Failing after five seconds
-- and being retried is the cheaper outcome.
--
-- statement_timeout is generous here because index builds on a large table
-- legitimately take minutes. It is unrelated to the 15s the application pool
-- sets on its own sessions.
SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- Identity: who may sign in, and the short-lived artefacts that let them.
--
-- Every id column reads `uuid PRIMARY KEY DEFAULT uuidv7()`. The application
-- still generates the value, because it needs the id before INSERT to write
-- the matching activity_events row inside the same transaction. The DEFAULT is
-- a safety net for manual inserts, seeds, and repair scripts. See ADR-0007.
--
-- Every index below carries `-- squawk-ignore require-concurrent-index-creation`.
-- The tables are created in this same file, so there are no rows and no other
-- sessions to block — and CONCURRENTLY could not run here anyway, because it
-- is not allowed inside a transaction. The rule stays armed everywhere else,
-- which is where it earns its keep: indexing a table that already has data.

CREATE TABLE users (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    email          text NOT NULL,
    name           text NOT NULL,
    password_hash  text NOT NULL,
    timezone       text NOT NULL DEFAULT 'Asia/Jakarta',
    is_owner       boolean NOT NULL DEFAULT false,
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    deactivated_at timestamptz,
    deleted_at     timestamptz,
    CONSTRAINT users_email_not_blank CHECK (btrim(email) <> ''),
    CONSTRAINT users_name_not_blank CHECK (btrim(name) <> '')
);

-- An expression index: queries must use lower(email) or this is not used at
-- all. Partial on deleted_at so a deleted address does not block re-use.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX users_email_key
    ON users (lower(email)) WHERE deleted_at IS NULL;

-- The glossary defines Owner as one person. Enforced here rather than in code:
-- every indexed value is `true`, so uniqueness allows at most one row.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX users_single_owner_key
    ON users (is_owner) WHERE is_owner AND deleted_at IS NULL;

CREATE TABLE sessions (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash   bytea NOT NULL,
    user_agent   text NOT NULL DEFAULT '',
    ip_hash      bytea,
    created_at   timestamptz NOT NULL DEFAULT now(),
    last_seen_at timestamptz NOT NULL DEFAULT now(),
    expires_at   timestamptz NOT NULL,
    CONSTRAINT sessions_token_hash_len CHECK (octet_length(token_hash) = 32)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX sessions_token_hash_key ON sessions (token_hash);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX sessions_user_id_idx ON sessions (user_id);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX sessions_expires_at_idx ON sessions (expires_at);

CREATE TABLE invitations (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    email       text NOT NULL,
    token_hash  bytea NOT NULL,
    invited_by  uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- The foreign key to projects is added in 00002, because projects does not
    -- exist yet. docs/data-model.md shows it inline; that reads well as a
    -- schema description but cannot be executed in that order.
    project_id  uuid,
    role        text NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    expires_at  timestamptz NOT NULL,
    accepted_at timestamptz,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT invitations_token_hash_len CHECK (octet_length(token_hash) = 32)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX invitations_token_hash_key ON invitations (token_hash);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX invitations_email_idx
    ON invitations (lower(email)) WHERE accepted_at IS NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX invitations_invited_by_idx ON invitations (invited_by);

CREATE TABLE password_resets (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    expires_at timestamptz NOT NULL,
    used_at    timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT password_resets_token_hash_len CHECK (octet_length(token_hash) = 32)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX password_resets_token_hash_key ON password_resets (token_hash);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX password_resets_user_id_idx ON password_resets (user_id);

-- Soft delete is enforced through a view rather than through discipline: every
-- generated query reads from _live, so one forgotten WHERE cannot leak deleted
-- rows.
--
-- Columns are listed explicitly on purpose. PostgreSQL expands SELECT * when
-- the view is created and then freezes it, so a view defined with * silently
-- stops showing columns added later. TestLiveViewsMatchTheirTables catches a
-- view that has fallen behind.
CREATE VIEW users_live AS
SELECT id,
       email,
       name,
       password_hash,
       timezone,
       is_owner,
       created_at,
       updated_at,
       deactivated_at,
       deleted_at
FROM users
WHERE deleted_at IS NULL;
