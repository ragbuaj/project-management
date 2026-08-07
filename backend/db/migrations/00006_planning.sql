-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- Notifications, time tracking, saved filters, and the automation engine.
-- Phases 2 through 7 read these; the schema is written now so that the
-- foreign keys are declared while every table they point at is empty.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`:
-- the tables are created here, so there is nothing to block, and CONCURRENTLY
-- cannot run inside a transaction. See .squawk.toml.

CREATE TABLE notifications (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id    uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- The activity_events row behind this. No foreign key, for the reason
    -- given in 00004: the target is partitioned on occurred_at, so a reference
    -- would drag that column in here to satisfy the constraint.
    event_id   uuid NOT NULL,
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN
               ('mention', 'assigned', 'due_soon', 'overdue', 'comment', 'status_changed')),
    payload    jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at timestamptz NOT NULL DEFAULT now(),
    read_at    timestamptz
);

-- The bell: one person's unread notifications, newest first. Partial, so it
-- stays the size of what is unread rather than of everything ever sent.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX notifications_unread_idx ON notifications (user_id, created_at DESC)
    WHERE read_at IS NULL;
-- Sweeping read notifications after 90 days (docs/data-model.md, retention).
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX notifications_read_idx ON notifications (read_at) WHERE read_at IS NOT NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX notifications_project_idx ON notifications (project_id);

CREATE TABLE notification_channels (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    user_id     uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    kind        text NOT NULL CHECK (kind IN ('telegram', 'email')),
    -- A Telegram chat_id or an email address. Personal data: never logged.
    address     text NOT NULL,
    -- Nothing is delivered here until this is set. An unverified address is
    -- an address someone else typed.
    verified_at timestamptz,
    prefs       jsonb NOT NULL DEFAULT '{}'::jsonb,
    created_at  timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT notification_channels_address_not_blank CHECK (btrim(address) <> '')
);

-- One channel per kind per person. Two Telegram chats for one account would
-- make "send it to them" ambiguous, and both would get everything.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX notification_channels_key ON notification_channels (user_id, kind);

CREATE TABLE saved_filters (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    owner_id   uuid NOT NULL REFERENCES users (id) ON DELETE CASCADE,
    -- NULL means the filter runs across every project the owner can see.
    project_id uuid REFERENCES projects (id) ON DELETE CASCADE,
    name       text NOT NULL,
    query      jsonb NOT NULL,
    is_shared  boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT saved_filters_name_not_blank CHECK (btrim(name) <> '')
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX saved_filters_owner_name_key ON saved_filters (owner_id, lower(name));
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX saved_filters_project_idx ON saved_filters (project_id) WHERE project_id IS NOT NULL;

CREATE TABLE time_logs (
    id               uuid PRIMARY KEY DEFAULT uuidv7(),
    card_id          uuid NOT NULL REFERENCES cards (id) ON DELETE CASCADE,
    -- RESTRICT, and it never fires: accounts are masked, not deleted. Someone
    -- else's time must not vanish from a report because they left.
    user_id          uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    started_at       timestamptz NOT NULL,
    ended_at         timestamptz,
    -- Stored rather than derived. Reports sum this across thousands of rows,
    -- and an interval subtraction per row is work repeated every time the
    -- chart is drawn. Kept honest by time_logs_duration below.
    -- squawk-ignore prefer-bigint-over-int
    duration_seconds integer CHECK (duration_seconds IS NULL OR duration_seconds > 0),
    -- Free text about what was worked on. Personal data: never logged.
    note             text NOT NULL DEFAULT '',
    created_at       timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT time_logs_period CHECK (ended_at IS NULL OR ended_at > started_at),
    -- A finished log has a duration and a running one does not. Without this
    -- a stopped timer could report nothing, and every sum would be quietly
    -- short.
    CONSTRAINT time_logs_duration CHECK (
        (ended_at IS NULL AND duration_seconds IS NULL)
        OR (ended_at IS NOT NULL AND duration_seconds IS NOT NULL))
);

-- One running timer per person. Two would both keep counting, and the second
-- one is always an accident.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX time_logs_one_running_key ON time_logs (user_id) WHERE ended_at IS NULL;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX time_logs_card_idx ON time_logs (card_id, started_at DESC);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX time_logs_user_idx ON time_logs (user_id, started_at DESC);

-- The table behind invariant 4 in 00003: a card's status may only move along
-- a transition declared here. That rule needs a read of this table, which is
-- why it cannot be a CHECK on cards.
CREATE TABLE workflow_transitions (
    id             uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id     uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- NULL means "from any status", which is how a project says a status is
    -- reachable from everywhere.
    from_status_id uuid REFERENCES statuses (id) ON DELETE CASCADE,
    to_status_id   uuid NOT NULL REFERENCES statuses (id) ON DELETE CASCADE,
    name           text NOT NULL DEFAULT '',
    conditions     jsonb NOT NULL DEFAULT '[]'::jsonb,
    CONSTRAINT workflow_transitions_not_circular CHECK (from_status_id IS DISTINCT FROM to_status_id)
);

-- A plain UNIQUE would let the "from any status" rule be declared twice, since
-- NULL is never equal to NULL. Folding NULL onto a fixed uuid closes that.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX workflow_transitions_key ON workflow_transitions (
    project_id,
    coalesce(from_status_id, '00000000-0000-0000-0000-000000000000'::uuid),
    to_status_id);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX workflow_transitions_to_idx ON workflow_transitions (to_status_id);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX workflow_transitions_from_idx ON workflow_transitions (from_status_id)
    WHERE from_status_id IS NOT NULL;

CREATE TABLE automation_rules (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       text NOT NULL,
    trigger    jsonb NOT NULL,
    conditions jsonb NOT NULL DEFAULT '[]'::jsonb,
    actions    jsonb NOT NULL,
    enabled    boolean NOT NULL DEFAULT true,
    created_by uuid NOT NULL REFERENCES users (id) ON DELETE RESTRICT,
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT automation_rules_name_not_blank CHECK (btrim(name) <> '')
);

-- The engine only ever looks for rules that are on.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX automation_rules_project_idx ON automation_rules (project_id) WHERE enabled;
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX automation_rules_created_by_idx ON automation_rules (created_by);

CREATE TABLE automation_runs (
    id       uuid PRIMARY KEY DEFAULT uuidv7(),
    rule_id  uuid NOT NULL REFERENCES automation_rules (id) ON DELETE CASCADE,
    event_id uuid NOT NULL,
    -- How many rules deep this run is. A rule whose action fires the trigger
    -- of another rule is a loop waiting to happen, and the cap below is what
    -- stops it in the database rather than in whoever is on call.
    -- squawk-ignore prefer-bigint-over-int
    depth    integer NOT NULL DEFAULT 0,
    state    text NOT NULL CHECK (state IN ('succeeded', 'failed', 'skipped')),
    error    text,
    ran_at   timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT automation_runs_depth_cap CHECK (depth BETWEEN 0 AND 5)
);

-- squawk-ignore require-concurrent-index-creation
CREATE INDEX automation_runs_rule_idx ON automation_runs (rule_id, ran_at DESC);
-- Failures have to be visible in the interface, not just in a log
-- (product-brief.md, "break loudly"). This is the index that view reads.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX automation_runs_failed_idx ON automation_runs (ran_at DESC) WHERE state = 'failed';

CREATE TABLE card_templates (
    id         uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    name       text NOT NULL,
    payload    jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT card_templates_name_not_blank CHECK (btrim(name) <> '')
);

-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX card_templates_project_name_key ON card_templates (project_id, lower(name));

CREATE TABLE recurring_cards (
    id          uuid PRIMARY KEY DEFAULT uuidv7(),
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    template    jsonb NOT NULL,
    -- RFC 5545 recurrence rule. Stored as written, because rewriting it into
    -- columns loses the cases the standard covers and this project does not.
    rrule       text NOT NULL,
    -- The rule is evaluated in this zone, not the server's. "Every Monday at
    -- 09:00" means the user's Monday morning, and that is a different instant
    -- twice a year in any zone with daylight saving.
    timezone    text NOT NULL,
    next_run_at timestamptz NOT NULL,
    last_run_at timestamptz,
    enabled     boolean NOT NULL DEFAULT true,
    CONSTRAINT recurring_cards_rrule_not_blank CHECK (btrim(rrule) <> ''),
    CONSTRAINT recurring_cards_timezone_not_blank CHECK (btrim(timezone) <> '')
);

-- Two more invariants the database cannot hold, listed here for the same
-- reason as the five in 00003 — a rule that lives only in a service is a rule
-- the next rewrite of that service loses:
--
--   1. timezone names a real zone. Checking it means reading
--      pg_timezone_names, and a CHECK cannot contain a subquery. Left
--      unchecked it fails at 09:00 on the Monday it was meant to fire, so the
--      service validates it on write.
--   2. rrule parses as RFC 5545. Same shape of problem, no parser in SQL.

-- squawk-ignore require-concurrent-index-creation
CREATE INDEX recurring_cards_due_idx ON recurring_cards (next_run_at) WHERE enabled;
