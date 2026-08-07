-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- History and delivery. ADR-0002 puts both in PostgreSQL: the business write,
-- the activity event, and the outbox row commit in one transaction, so there
-- is no state where a card changed and its history did not.
--
-- Every index carries `-- squawk-ignore require-concurrent-index-creation`:
-- the tables are created here, so there is nothing to block, and CONCURRENTLY
-- cannot run inside a transaction — nor on a partitioned parent at all. See
-- .squawk.toml.

-- Partitioned by month from the first row. This is the only table in the
-- system that grows without a bound, and partitioning one that is already
-- large means rewriting it; partitioning an empty one is free. Retention drops
-- whole partitions after 24 months, which is a catalogue update rather than a
-- 50-million-row DELETE (docs/data-model.md, retention).
--
-- The primary key has to carry occurred_at: PostgreSQL requires the partition
-- key inside every unique constraint, because uniqueness is enforced per
-- partition and it cannot see across them.
CREATE TABLE activity_events (
    id          uuid NOT NULL DEFAULT uuidv7(),
    project_id  uuid NOT NULL REFERENCES projects (id) ON DELETE CASCADE,
    -- NULL means the system acted, not a person: a scheduled job, an
    -- automation rule, or an inbound webhook.
    actor_id    uuid REFERENCES users (id) ON DELETE SET NULL,
    entity_type text NOT NULL CHECK (entity_type IN
                ('card', 'board', 'column', 'status', 'comment', 'sprint', 'project',
                 'checklist', 'label', 'member', 'automation', 'time_log', 'vcs_link')),
    entity_id   uuid NOT NULL,
    -- 'card.moved', 'comment.created'. Deliberately not a CHECK: the list grows
    -- with every phase, and a constraint here would turn each new event type
    -- into a migration.
    action      text NOT NULL,
    -- Can hold titles, comment bodies, and names. Never logged; see
    -- docs/data-model.md on which columns must stay out of application logs.
    payload     jsonb NOT NULL DEFAULT '{}'::jsonb,
    occurred_at timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT activity_events_action_not_blank CHECK (btrim(action) <> ''),
    PRIMARY KEY (id, occurred_at)
) PARTITION BY RANGE (occurred_at);

-- Declared on the parent, which creates them on every partition that exists
-- now and every one attached later. DESC because every reader of this table
-- wants the newest first.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX activity_events_entity_idx ON activity_events (entity_type, entity_id, occurred_at DESC);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX activity_events_project_idx ON activity_events (project_id, occurred_at DESC);
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX activity_events_actor_idx ON activity_events (actor_id, occurred_at DESC);

-- The partition every row falls into when no monthly partition covers it.
--
-- Without it an event outside every range is rejected, and because the event
-- is written in the same transaction as the change that caused it, that
-- rejection fails the user's request. A history table must never be able to
-- take the application down.
--
-- The cost is real and worth stating: a month partition cannot be attached
-- while DEFAULT holds rows belonging to that month. Recovery is to move those
-- rows out, attach, and move them back. The monthly job runs three months
-- ahead precisely so this stays a broken-job scenario rather than a routine
-- one.
CREATE TABLE activity_events_default PARTITION OF activity_events DEFAULT;

-- +goose StatementBegin
-- Partitions for this month and the next three. The dates are computed at
-- migration time rather than written as literals: a database created a year
-- from now needs partitions around that day, not around the day this file was
-- written.
--
-- Phase 1 gives the worker a monthly job that extends the window. Until then
-- these four months are the runway, and DEFAULT above is what happens if
-- nobody builds the job in time.
--
-- Boundaries are pinned to UTC rather than to the session's TimeZone, so that
-- a partition named 2026_08 holds exactly the UTC month of that name. Left to
-- the session setting, the same migration would cut months at different
-- instants on different servers, and changing postgresql.conf later would put
-- the naming and the ranges quietly out of step.
DO $$
DECLARE
    start_month timestamptz := date_trunc('month', now() AT TIME ZONE 'UTC') AT TIME ZONE 'UTC';
    month_start timestamptz;
BEGIN
    FOR i IN 0..3 LOOP
        month_start := start_month + (i || ' months')::interval;

        EXECUTE format(
            'CREATE TABLE %I PARTITION OF activity_events FOR VALUES FROM (%L) TO (%L)',
            'activity_events_' || to_char(month_start AT TIME ZONE 'UTC', 'YYYY_MM'),
            month_start,
            month_start + interval '1 month'
        );
    END LOOP;
END
$$;
-- +goose StatementEnd

-- The transactional outbox of ADR-0002. A row here is a promise that something
-- happened; the worker turns it into a notification, a webhook, or a websocket
-- frame after the transaction commits, because external calls inside a
-- transaction hold locks for as long as the other end feels like taking.
CREATE TABLE outbox (
    -- bigint identity rather than uuid: delivery order matters here and these
    -- ids are never shown to anyone. GENERATED ALWAYS, not BY DEFAULT, so no
    -- caller can insert an id and reorder the queue.
    id           bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    -- The activity_events row this came from. No foreign key: the target is
    -- partitioned and its key is (id, occurred_at), so a reference would drag
    -- occurred_at into this table purely to satisfy the constraint. Not every
    -- outbox row has an event behind it either — some are pure delivery.
    event_id     uuid NOT NULL,
    topic        text NOT NULL,
    payload      jsonb NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    published_at timestamptz,
    -- Bounded: the worker gives up and dead-letters long before this could
    -- overflow, so bigint would say something untrue about the domain.
    -- squawk-ignore prefer-bigint-over-int
    attempts     integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    last_error   text,
    CONSTRAINT outbox_topic_not_blank CHECK (btrim(topic) <> '')
);

-- The worker reads only what it has not sent, so the index stays the size of
-- the backlog rather than the size of the table. Ordered by id, which is the
-- delivery order.
--
-- Queue depth read through this index is the first health metric in the
-- system: ADR-0002 alarms above 1,000 rows pending for more than a minute.
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX outbox_pending_idx ON outbox (id) WHERE published_at IS NULL;

-- Sweeping delivered rows after seven days (docs/data-model.md, retention).
-- squawk-ignore require-concurrent-index-creation
CREATE INDEX outbox_published_idx ON outbox (published_at) WHERE published_at IS NOT NULL;
