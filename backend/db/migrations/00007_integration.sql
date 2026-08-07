-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- The tables that hold credentials and the tables that let something outside
-- this system in: API tokens, public board links, and the VCS connection.
--
-- Everything secret here is stored as a hash or as ciphertext. Nothing in this
-- file can be read back into the value someone was given, which is the point:
-- a database dump of these tables grants nobody anything.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`:
-- the tables are created here, so there is nothing to block, and CONCURRENTLY
-- cannot run inside a transaction. See .squawk.toml.

CREATE TABLE api_tokens (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id      uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- What the person called it, so they can tell two tokens apart when
    -- revoking one. Never the token.
    name         text NOT NULL,
    -- SHA-256 of the token. bytea rather than text: it is 32 bytes, not
    -- characters, and no encoding step means no encoding mismatch when
    -- comparing. Argon2id is for passwords, which are low-entropy and
    -- guessable; a generated token is neither, so a fast hash is right here
    -- and a slow one would only cost every request.
    token_hash   bytea NOT NULL,
    scopes       text[] NOT NULL CHECK (cardinality(scopes) > 0),
    -- NOT NULL: a token that never expires is a credential nobody remembers
    -- issuing. Rotation has to be forced by something.
    expires_at   timestamptz NOT NULL,
    last_used_at timestamptz,
    revoked_at   timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT api_tokens_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT api_tokens_token_hash_len CHECK (octet_length(token_hash) = 32)
);

-- The lookup every authenticated API request makes: hash the presented token,
-- find the row. Unique, so a hash collision cannot silently authenticate the
-- wrong account.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX api_tokens_hash_key ON api_tokens (token_hash);
-- The token list in settings, and the revocation sweep. Only live tokens.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX api_tokens_user_idx ON api_tokens (user_id) WHERE revoked_at IS NULL;

CREATE TABLE share_links (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    board_id   uuid NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    token_hash bytea NOT NULL,
    -- RESTRICT, and it never fires: accounts are masked, not deleted. Who
    -- published a board is part of the audit trail.
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- Nullable here, unlike api_tokens: a share link is scoped to one board
    -- and read-only, and a permanent link to a public board is a thing people
    -- legitimately want. Revocation is the control that matters.
    expires_at timestamptz,
    revoked_at timestamptz,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT share_links_token_hash_len CHECK (octet_length(token_hash) = 32)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX share_links_hash_key ON share_links (token_hash);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX share_links_board_idx ON share_links (board_id) WHERE revoked_at IS NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX share_links_created_by_idx ON share_links (created_by);

CREATE TABLE vcs_connections (
    id                 uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id         uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    provider           text NOT NULL CHECK (provider IN ('github', 'gitlab')),
    repo_full_name     text NOT NULL,
    -- AES-GCM ciphertext, key from the environment. Encrypted rather than
    -- hashed because this one has to be replayed: the system presents it to
    -- GitHub on every call. Never logged, never returned by any endpoint.
    credential_enc     bytea NOT NULL,
    -- Same, and it is what proves an inbound webhook really came from the
    -- provider.
    webhook_secret_enc bytea NOT NULL,
    created_at         timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT vcs_connections_repo_format CHECK (repo_full_name ~ '^[^/ ]+/[^/ ]+$')
);

-- One connection per repository per provider per project. A second would
-- double every webhook and every sync.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX vcs_connections_key ON vcs_connections (project_id, provider, repo_full_name);

CREATE TABLE vcs_links (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    card_id       uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    connection_id uuid NOT NULL REFERENCES vcs_connections (id) ON DELETE CASCADE,
    kind          text NOT NULL CHECK (kind IN ('issue', 'change_request', 'branch', 'commit')),
    -- The provider's id for the thing, as the provider spells it. Text, not
    -- bigint: a branch is named, a commit is a sha, and only issues and merge
    -- requests are numbers (ADR-0006).
    external_id   text NOT NULL,
    url           text NOT NULL,
    -- Provider vocabulary, deliberately not constrained. 'unknown' until a
    -- sync says otherwise; GitHub and GitLab disagree about the words, and
    -- ADR-0006 keeps that disagreement out of the schema.
    state         text NOT NULL DEFAULT 'unknown',
    synced_at     timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT vcs_links_external_id_not_blank CHECK (btrim(external_id) <> ''),
    CONSTRAINT vcs_links_url_is_https CHECK (url LIKE 'https://%')
);

-- One row per (connection, kind, external thing, card). The card is part of
-- the key because one merge request may legitimately close several cards.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX vcs_links_key ON vcs_links (connection_id, kind, external_id, card_id);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX vcs_links_card_idx ON vcs_links (card_id);

CREATE TABLE vcs_webhook_deliveries (
    id            uuid PRIMARY KEY DEFAULT uuidv7(),
    connection_id uuid NOT NULL REFERENCES vcs_connections (id) ON DELETE CASCADE,
    -- X-GitHub-Delivery or X-Gitlab-Event-UUID.
    delivery_id   text NOT NULL,
    raw_body      jsonb NOT NULL,
    received_at   timestamptz NOT NULL DEFAULT now(),
    processed_at  timestamptz,
    error         text,
    CONSTRAINT vcs_webhook_deliveries_id_not_blank CHECK (btrim(delivery_id) <> '')
);

-- Idempotency, and it is not optional: providers redeliver on any doubt about
-- the response, so the same push arrives more than once as a matter of course.
-- Without this the second copy creates a second set of links.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX vcs_webhook_deliveries_key ON vcs_webhook_deliveries (connection_id, delivery_id);
-- The retry sweep and the backlog view: what arrived and was never processed.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX vcs_webhook_deliveries_pending_idx ON vcs_webhook_deliveries (received_at)
    WHERE processed_at IS NULL;
