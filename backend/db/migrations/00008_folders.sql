-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- Folders: a container for projects, and a second place a person can be a
-- member of.
--
-- ADR-0011 is the shape behind this file. One level — a folder holds projects,
-- never other folders — and membership is inherited: someone's effective role
-- on a project is the higher of their project role and their folder role.
-- internal/authz.EffectiveRole is the only code allowed to know that.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`,
-- including the one on projects, which is an existing table. The reason is at
-- that index. See .squawk.toml.

-- No deleted_at, and therefore no folders_live view: a folder owns no content
-- of its own. Deleting one releases its projects rather than taking them with
-- it, so there is nothing here that a trash-and-restore would protect.
--
-- No unique name either. A folder is only ever seen by its members, so two
-- unrelated people naming theirs "Klien" is not a collision — and making it
-- one would let a stranger's folder decide what someone may call their own.
CREATE TABLE folders (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    name       text NOT NULL,
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT folders_name_not_blank CHECK (btrim(name) <> '')
);

-- squawk-ignore require-concurrent-index-creation
CREATE INDEX folders_created_by_idx ON folders (created_by);

-- The second source of truth for authorization, alongside project_members.
-- The role values are deliberately the same three: a folder role means exactly
-- what the same project role means, applied to everything inside.
CREATE TABLE folder_members (
    folder_id uuid NOT NULL REFERENCES folders (id) ON DELETE CASCADE,
    user_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    role      text NOT NULL CHECK (role IN ('admin', 'member', 'viewer')),
    added_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (folder_id, user_id)
);

-- The primary key already covers folder_id. This covers the direction every
-- permission check asks: "which folders does this user belong to".
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX folder_members_user_id_idx ON folder_members (user_id);

-- NULL means the project stands on its own (ADR-0011). Added as a plain
-- nullable column first: a nullable column with no default is metadata-only,
-- while ADD COLUMN ... REFERENCES would take the lock and validate in one go.
ALTER TABLE projects ADD COLUMN folder_id uuid;

-- ON DELETE SET NULL is the database enforcing the promise in ADR-0011:
-- deleting a folder must never delete the work inside it. Leaving it to the
-- service would make "the folder was deleted" and "the projects survived" two
-- separate things to remember.
--
-- NOT VALID skips the scan of existing rows while taking only a light lock,
-- and VALIDATE then checks them without blocking writes. Every row is NULL
-- today so either would do; written this way because it is the pattern the
-- next migration will copy.
ALTER TABLE projects
    ADD CONSTRAINT projects_folder_id_fkey
    FOREIGN KEY (folder_id) REFERENCES folders (id) ON DELETE SET NULL NOT VALID;

ALTER TABLE projects VALIDATE CONSTRAINT projects_folder_id_fkey;

-- Answers "which project is in this folder" and, on every permission check,
-- "which folder is this project in".
--
-- projects is an existing table, so require-concurrent-index-creation would
-- normally apply — .squawk.toml says an index on an existing table belongs in
-- its own NO TRANSACTION file with CONCURRENTLY. It is ignored here, and the
-- reason is worth writing down because it was learned the expensive way.
--
-- The rule exists to stop a long index build from holding a lock on a busy
-- table. This index has nothing to build: folder_id was added a few statements
-- above, so it is NULL in every row, and the index is partial on
-- folder_id IS NOT NULL. It covers zero rows by construction.
--
-- What CONCURRENTLY costs here is worse than what it saves. It cannot run in a
-- transaction, so a failure cannot be rolled back — and a failure is not
-- hypothetical: it waits for every transaction that can see projects to
-- finish, which under lock_timeout means it is cancelled the moment anything
-- else holds one. It then leaves an INVALID index behind, and every later
-- migration fails with "already exists" until somebody drops it by hand. That
-- happened on the first run of this file, against a fresh database, with the
-- test packages migrating in parallel — the same shape as a rolling deploy.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX projects_folder_id_idx ON projects (folder_id) WHERE folder_id IS NOT NULL;

-- The view froze its column list when it was created, so a new column on the
-- table does not appear here on its own — and a _live view that quietly stops
-- showing a column is how a soft-delete leak begins.
-- TestLiveViewsMatchTheirTables is what refuses to let that happen; folder_id
-- goes last because CREATE OR REPLACE VIEW may only append.
CREATE OR REPLACE VIEW projects_live AS
SELECT id,
       key,
       name,
       description,
       card_seq,
       created_by,
       created_at,
       updated_at,
       archived_at,
       deleted_at,
       folder_id
FROM projects
WHERE deleted_at IS NULL;
