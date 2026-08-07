-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- Sprints and cards. This is the table the rest of the product hangs off, so
-- the columns for phases that have not started yet are declared now, nullable
-- and unused: adding a column to an empty table costs nothing, adding one to a
-- table holding every card in the system is a migration with a lock plan.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`:
-- the tables are created here, so there is nothing to block, and CONCURRENTLY
-- cannot run inside a transaction. See .squawk.toml.

-- Created before cards because cards.sprint_id points at it. Nothing reads it
-- until Phase 4.
CREATE TABLE sprints (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id   uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name         text NOT NULL,
    goal         text NOT NULL DEFAULT '',
    state        text NOT NULL DEFAULT 'planned'
                 CHECK (state IN ('planned', 'active', 'completed')),
    start_at     timestamptz,
    end_at       timestamptz,
    completed_at timestamptz,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT sprints_name_not_blank CHECK (btrim(name) <> ''),
    CONSTRAINT sprints_period_order CHECK (
        start_at IS NULL OR end_at IS NULL OR start_at < end_at),
    -- A sprint nobody can date is a sprint no burndown chart can draw.
    CONSTRAINT sprints_active_has_period CHECK (
        state <> 'active' OR (start_at IS NOT NULL AND end_at IS NOT NULL))
);

-- One active sprint per project. Two would make "the current sprint"
-- ambiguous in every query that asks for it.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX sprints_one_active_key ON sprints (project_id) WHERE state = 'active';
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX sprints_project_idx ON sprints (project_id, state);

CREATE TABLE cards (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id      uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- The 142 in PM-142. Allocated from projects.card_seq, not a sequence,
    -- because this number is human-facing and a rolled-back transaction must
    -- not leave a hole in it.
    number          bigint NOT NULL,
    type            text NOT NULL DEFAULT 'task'
                    CHECK (type IN ('epic', 'story', 'task', 'bug', 'subtask')),
    title           text NOT NULL CHECK (length(title) BETWEEN 1 AND 500),
    description     text NOT NULL DEFAULT '',
    status_id       uuid NOT NULL,
    -- Fractional index, binary collation (ADR-0003). One drag updates one row
    -- whatever the column holds.
    position        text COLLATE "C" NOT NULL,
    priority        text NOT NULL DEFAULT 'medium'
                    CHECK (priority IN ('low', 'medium', 'high', 'urgent')),
    assignee_id     uuid REFERENCES users (id) ON DELETE SET NULL,
    reporter_id     uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- Declared now, written from Phase 4 (sprint, epic) and Phase 5 (dates).
    -- No ON DELETE clause, so NO ACTION: see the note on the composite key
    -- below. Deleting a card that still has subtasks is refused either way.
    parent_card_id  uuid REFERENCES cards (id),
    epic_id         uuid REFERENCES cards (id) ON DELETE SET NULL,
    sprint_id       uuid REFERENCES sprints (id) ON DELETE SET NULL,
    estimate_points numeric(6, 2) CHECK (estimate_points IS NULL OR estimate_points >= 0),
    start_date      date,
    due_date        date,
    -- Set when the card reaches a status whose category is 'done', by the
    -- service. Cycle-time reporting reads this, never the status name.
    completed_at    timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    archived_at     timestamptz,
    deleted_at      timestamptz,
    -- STORED is spelled out because PostgreSQL 18 made VIRTUAL the default for
    -- generated columns (ADR-0007), and a virtual column cannot carry a GIN
    -- index. Dropping the keyword would leave search silently unindexed.
    --
    -- 'simple' rather than a language configuration: the content is mixed
    -- Indonesian and English, and no stemmer handles both. Stemming the wrong
    -- language loses matches that a plain lexeme split would have found.
    search_tsv      tsvector GENERATED ALWAYS AS (
                        to_tsvector('simple', coalesce(title, '') || ' ' || coalesce(description, ''))
                    ) STORED,
    CONSTRAINT cards_position_not_blank CHECK (position <> ''),
    CONSTRAINT cards_number_positive CHECK (number > 0),
    CONSTRAINT cards_dates_order CHECK (
        start_date IS NULL OR due_date IS NULL OR start_date <= due_date),
    CONSTRAINT cards_no_self_parent CHECK (parent_card_id IS DISTINCT FROM id),
    CONSTRAINT cards_no_self_epic CHECK (epic_id IS DISTINCT FROM id),
    -- The one invariant on this table the database can enforce, and the most
    -- expensive one to leave to the service: a card carrying a status that
    -- belongs to another project vanishes from every board without any error
    -- being raised anywhere.
    --
    -- This is the only foreign key on status_id. A second, single-column one
    -- would enforce nothing the composite key does not already enforce, while
    -- costing a second check on every insert. It also still refuses to delete
    -- a status any card holds, which is what that second key was for.
    --
    -- No ON DELETE clause, so NO ACTION rather than RESTRICT. The two differ
    -- only when one statement deletes the referenced row and the referencing
    -- row together, which is what the retention job does when it removes a
    -- project: NO ACTION checks once the statement is finished and sees an
    -- empty table, RESTRICT checks per row and depends on cards happening to
    -- be deleted before statuses. That order is real but not promised, and a
    -- constraint resting on it would fail the day the promise changes.
    CONSTRAINT cards_status_same_project
        FOREIGN KEY (status_id, project_id) REFERENCES statuses (id, project_id)
);

-- The five invariants this table needs and the database cannot hold. They are
-- listed here, next to the columns they constrain, because an invariant that
-- lives only in a service is an invariant that gets forgotten during the next
-- rewrite of that service. Each one is enforced in modules/project with a test
-- named after it:
--
--   1. epic_id points at a card of type 'epic' in the same project.
--      A CHECK cannot contain a subquery.
--   2. parent_card_id forms no cycle. Needs recursive traversal.
--   3. Subtask nesting is at most one level deep. Needs a read of the parent.
--   4. Status changes follow the transitions allowed by workflow_transitions
--      (E5). Needs a read of another table.
--   5. completed_at is set exactly when the card's status has category 'done'.
--      Needs a read of statuses, and the rule is directional: leaving 'done'
--      clears it.
--
-- Only the sixth — status_id belongs to project_id — is a constraint, above.

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX cards_project_number_key ON cards (project_id, number);

-- Order is unique per (project, status) among the cards a board actually
-- shows. ADR-0003 scopes ordering to the project rather than the board, so
-- several boards over one project share it.
--
-- This index also serves the board scan itself; there is no second index for
-- reading a column.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX cards_order_key ON cards (project_id, status_id, position)
    WHERE deleted_at IS NULL AND archived_at IS NULL;

-- Serves the composite foreign key. cards_order_key cannot: it leads with
-- project_id and is partial, so deleting a status would scan every card.
-- Statuses are hard-deleted — unlike projects and cards, they carry no
-- deleted_at — which makes this the one parent deletion that really happens.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_status_idx ON cards (status_id, project_id);

-- My Tasks: one person's open cards, soonest first.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_assignee_due_idx ON cards (assignee_id, due_date)
    WHERE deleted_at IS NULL AND archived_at IS NULL AND assignee_id IS NOT NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_sprint_idx ON cards (sprint_id) WHERE sprint_id IS NOT NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_epic_idx ON cards (epic_id) WHERE epic_id IS NOT NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_parent_idx ON cards (parent_card_id) WHERE parent_card_id IS NOT NULL;
-- Due-date sweeps: the overdue badge and the notification job.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_due_idx ON cards (due_date)
    WHERE due_date IS NOT NULL AND deleted_at IS NULL AND archived_at IS NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX cards_search_idx ON cards USING GIN (search_tsv);

-- assignee_id and reporter_id are deliberately left without an index of their
-- own. An account is never removed: deleting one fills deleted_at and masks
-- the address, and its cards stay (docs/data-model.md, retention). The parent
-- deletion those indexes would speed up does not occur.

-- Soft-delete view. Columns are listed explicitly because PostgreSQL freezes
-- SELECT * at creation; TestLiveViewsMatchTheirTables proves it stays in step.
CREATE VIEW cards_live AS
SELECT id,
       project_id,
       number,
       type,
       title,
       description,
       status_id,
       position,
       priority,
       assignee_id,
       reporter_id,
       parent_card_id,
       epic_id,
       sprint_id,
       estimate_points,
       start_date,
       due_date,
       completed_at,
       created_at,
       updated_at,
       archived_at,
       deleted_at,
       search_tsv
FROM cards
WHERE deleted_at IS NULL;
