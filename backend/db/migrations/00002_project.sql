-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- Project and the things a project owns: who belongs to it, what states its
-- cards can be in, and how those states are laid out on a board.
--
-- ADR-0004 is the shape behind this file. Status belongs to the project and is
-- what a card actually is; a board column is presentation. A card keeps its
-- status whether or not any board shows it, which is what lets the table,
-- calendar, and timeline views exist at all.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`:
-- the tables are created here, so there is nothing to block, and CONCURRENTLY
-- cannot run inside a transaction. See .squawk.toml.

CREATE TABLE projects (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    key         text NOT NULL,
    name        text NOT NULL,
    description text NOT NULL DEFAULT '',
    -- Numbers cards as PM-142. Bumped under SELECT ... FOR UPDATE, because
    -- a sequence would leave gaps whenever a transaction rolls back and the
    -- number is meant to be human-facing.
    card_seq    bigint NOT NULL DEFAULT 0,
    created_by  uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    deleted_at  timestamptz,
    CONSTRAINT projects_key_format CHECK (key ~ '^[A-Z][A-Z0-9]{1,9}$'),
    CONSTRAINT projects_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT projects_card_seq_not_negative CHECK (card_seq >= 0)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX projects_key_key ON projects (key) WHERE deleted_at IS NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX projects_created_by_idx ON projects (created_by);

-- The membership table is the whole of authorization. docs/authorization.md
-- derives every rule from a row here, and internal/authz is the only place
-- that reads it.
CREATE TABLE project_members (
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role       text NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    added_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (project_id, user_id)
);

-- The primary key already covers project_id. This covers the other direction:
-- "which projects does this user belong to", which every authorization check
-- and the My Tasks view both ask.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX project_members_user_id_idx ON project_members (user_id);

CREATE TABLE statuses (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       text NOT NULL,
    -- The source of truth for "done", not the status name. Reports and the
    -- overdue badge read this, so that someone creating a status called
    -- "Done ✅" does not quietly stop every chart from counting it.
    category   text NOT NULL CHECK (category IN ('todo', 'in_progress', 'done')),
    position   text COLLATE "C" NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT statuses_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT statuses_position_not_blank CHECK (position <> ''),
    -- Lets 00003 point cards at (status_id, project_id) as one foreign key,
    -- which makes it impossible for a card to carry a status belonging to
    -- another project. That is the cheapest kind of bug to prevent and one of
    -- the most expensive to find: the card simply disappears from every board.
    --
    -- Declared inline rather than added afterwards. ALTER TABLE ... ADD
    -- CONSTRAINT UNIQUE takes an ACCESS EXCLUSIVE lock and builds the index
    -- while holding it, which blocks reads as well as writes; inside CREATE
    -- TABLE there is nothing to lock.
    CONSTRAINT statuses_id_project_key UNIQUE (id, project_id)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX statuses_project_name_key ON statuses (project_id, lower(name));
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX statuses_project_position_idx ON statuses (project_id, position);

CREATE TABLE boards (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name        text NOT NULL,
    position    text COLLATE "C" NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    archived_at timestamptz,
    deleted_at  timestamptz,
    CONSTRAINT boards_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT boards_position_not_blank CHECK (position <> '')
);

-- squawk-ignore require-concurrent-index-creation
CREATE INDEX boards_project_idx ON boards (project_id, position) WHERE deleted_at IS NULL;

CREATE TABLE board_columns (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    board_id   uuid NOT NULL REFERENCES boards (id) ON DELETE CASCADE,
    -- RESTRICT, not CASCADE: deleting a status that a column still shows is a
    -- mistake worth refusing. Deleting the column, by contrast, touches no
    -- cards at all — they keep their status and simply stop being displayed
    -- here (ADR-0004).
    status_id  uuid NOT NULL REFERENCES statuses (id) ON DELETE RESTRICT,
    name       text NOT NULL,
    position   text COLLATE "C" NOT NULL,
    -- prefer-bigint-over-int guards surrogate keys and counters, which grow
    -- until they overflow. This is a bounded configuration value — "at most N
    -- cards in this column" — where realistic values are single digits, so
    -- bigint would say something untrue about the domain.
    -- squawk-ignore prefer-bigint-over-int
    wip_limit  integer CHECK (wip_limit IS NULL OR wip_limit > 0),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT board_columns_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT board_columns_position_not_blank CHECK (position <> '')
);

-- One column per status per board. Two columns showing the same status would
-- make a card appear twice on one board.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX board_columns_board_status_key ON board_columns (board_id, status_id);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX board_columns_status_idx ON board_columns (status_id);

CREATE TABLE labels (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       text NOT NULL,
    color      text NOT NULL CHECK (color ~ '^#[0-9a-f]{6}$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT labels_name_not_blank CHECK (btrim(name) <> '')
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX labels_project_name_key ON labels (project_id, lower(name));

-- The foreign key 00001 could not declare, because projects did not exist yet.
--
-- NOT VALID skips the scan of existing rows while taking only a light lock,
-- and VALIDATE then checks them without blocking writes. The table is empty
-- today so either would do; it is written this way because it is the pattern
-- every later migration will need, and patterns are copied from whatever came
-- before.
ALTER TABLE invitations
    ADD CONSTRAINT invitations_project_id_fkey
    FOREIGN KEY (project_id) REFERENCES projects (id) ON DELETE CASCADE NOT VALID;

ALTER TABLE invitations VALIDATE CONSTRAINT invitations_project_id_fkey;

-- squawk-ignore require-concurrent-index-creation
CREATE INDEX invitations_project_id_idx ON invitations (project_id)
    WHERE project_id IS NOT NULL;

-- Soft-delete views. Columns are listed explicitly because PostgreSQL freezes
-- SELECT * at creation; TestLiveViewsMatchTheirTables proves they stay in step.
CREATE VIEW projects_live AS
SELECT id,
       key,
       name,
       description,
       card_seq,
       created_by,
       created_at,
       updated_at,
       archived_at,
       deleted_at
FROM projects
WHERE deleted_at IS NULL;

CREATE VIEW boards_live AS
SELECT id,
       project_id,
       name,
       position,
       created_at,
       updated_at,
       archived_at,
       deleted_at
FROM boards
WHERE deleted_at IS NULL;
