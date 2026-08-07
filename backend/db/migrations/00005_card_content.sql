-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- What a card accumulates once people work on it: discussion, checklists,
-- relationships to other cards, and labels. Nothing reads these until Phase 2
-- and Phase 3; they are written now because the tables they hang off exist now
-- and an empty table is the cheapest place to get a foreign key wrong.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`:
-- the tables are created here, so there is nothing to block, and CONCURRENTLY
-- cannot run inside a transaction. See .squawk.toml.

CREATE TABLE comments (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    card_id    uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    -- RESTRICT, and it never fires: an account is not deleted, it is masked
    -- (docs/data-model.md, retention). That is a product decision — removing
    -- someone's comments when they leave destroys a shared record of the work.
    author_id  uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    -- Can hold anything anyone types. Never logged.
    body       text NOT NULL CHECK (length(body) BETWEEN 1 AND 20000),
    created_at timestamptz NOT NULL DEFAULT now(),
    edited_at  timestamptz,
    deleted_at timestamptz
);

-- The card's discussion, oldest first, which is the only way this is ever
-- read. Partial, because a deleted comment is never shown.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX comments_card_idx ON comments (card_id, created_at) WHERE deleted_at IS NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX comments_author_idx ON comments (author_id);

CREATE TABLE checklists (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    card_id    uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    title      text NOT NULL DEFAULT 'Checklist',
    -- Same fractional index as cards and statuses (ADR-0003). One ordering
    -- mechanism in this schema, not two.
    position   text COLLATE "C" NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT checklists_title_not_blank CHECK (btrim(title) <> ''),
    CONSTRAINT checklists_position_not_blank CHECK (position <> '')
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX checklists_card_position_key ON checklists (card_id, position);

CREATE TABLE checklist_items (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    checklist_id uuid NOT NULL REFERENCES checklists (id) ON DELETE CASCADE,
    content      text NOT NULL CHECK (length(content) BETWEEN 1 AND 1000),
    position     text COLLATE "C" NOT NULL,
    assignee_id  uuid REFERENCES users (id) ON DELETE SET NULL,
    due_date     date,
    completed_at timestamptz,
    completed_by uuid REFERENCES users (id) ON DELETE SET NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT checklist_items_position_not_blank CHECK (position <> ''),
    -- Someone recorded as having ticked the item implies the item is ticked.
    -- The reverse is deliberately allowed: ON DELETE SET NULL empties
    -- completed_by when an account is masked, and a done item must not become
    -- undone because of that.
    CONSTRAINT checklist_items_completed_by_needs_a_time CHECK (
        completed_by IS NULL OR completed_at IS NOT NULL)
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX checklist_items_list_position_key ON checklist_items (checklist_id, position);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX checklist_items_assignee_idx ON checklist_items (assignee_id)
    WHERE assignee_id IS NOT NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX checklist_items_completed_by_idx ON checklist_items (completed_by)
    WHERE completed_by IS NOT NULL;

CREATE TABLE card_links (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    from_card_id uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    to_card_id   uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    type         text NOT NULL CHECK (type IN ('blocks', 'relates_to', 'duplicates')),
    created_by   uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT card_links_not_self CHECK (from_card_id <> to_card_id)
);

-- 'relates_to' and 'duplicates' mean the same thing in both directions but are
-- stored once. Reads union the two directions; writes refuse the mirrored
-- duplicate in the service, because that check needs to compare a candidate
-- against a row it has to go and find. Storing two rows for one relationship
-- would be a second place for them to disagree.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX card_links_unique ON card_links (from_card_id, to_card_id, type);
-- Reading the other direction, which is half of every relationship panel.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX card_links_to_idx ON card_links (to_card_id);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX card_links_created_by_idx ON card_links (created_by);

CREATE TABLE card_labels (
    card_id  uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    label_id uuid NOT NULL REFERENCES labels (id) ON DELETE CASCADE,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (card_id, label_id)
);

-- The primary key covers card_id. This covers the other direction: "every card
-- carrying this label", which is what the board filter asks.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX card_labels_label_idx ON card_labels (label_id);

-- Soft-delete view. Columns are listed explicitly because PostgreSQL freezes
-- SELECT * at creation; TestLiveViewsMatchTheirTables proves it stays in step.
CREATE VIEW comments_live AS
SELECT id,
       card_id,
       author_id,
       body,
       created_at,
       edited_at,
       deleted_at
FROM comments
WHERE deleted_at IS NULL;
