-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- Rights move from the membership to the account (ADR-0012).
--
-- Before this, someone could be admin of one project and viewer of another.
-- The people using this installation are employees whose position does not
-- change when they move between projects, so the position lives on the account
-- and membership only says which folders and projects are within reach.
--
-- This is the **expand** half of expand-migrate-contract. users.is_owner is
-- deliberately still here: the sqlc queries in the identity module select it,
-- and dropping it in the same migration would leave every query in main broken
-- until the module catches up. It goes in 00010, after the code has moved.

-- 'contributor' as the default is the safe end of the range: an account created by
-- something that has not been taught about roles yet can neither create
-- workspaces nor see anything it was not added to.
ALTER TABLE users ADD COLUMN role text NOT NULL DEFAULT 'contributor';

ALTER TABLE users
    ADD CONSTRAINT users_role_known
    CHECK (role IN ('owner', 'maintainer', 'contributor', 'viewer')) NOT VALID;

ALTER TABLE users VALIDATE CONSTRAINT users_role_known;

UPDATE users SET role = 'owner' WHERE is_owner;

-- Two columns answering "who is the owner" are two columns that will one day
-- answer it differently. They coexist only until 00010 drops is_owner, and
-- this constraint is what keeps them honest in the meantime — including
-- against a hand-written UPDATE during the transition.
ALTER TABLE users
    ADD CONSTRAINT users_owner_columns_agree
    CHECK ((role = 'owner') = is_owner) NOT VALID;

ALTER TABLE users VALIDATE CONSTRAINT users_owner_columns_agree;

-- The glossary defines Owner as one person. The rule does not change, only the
-- column it reads.
-- squawk-ignore require-concurrent-index-creation
CREATE UNIQUE INDEX users_single_owner_role_key
    ON users ((role = 'owner')) WHERE role = 'owner' AND deleted_at IS NULL;

-- And the old one goes now, not in 00010 with the column it reads.
--
-- Leaving both would mean the rule is enforced twice, which sounds harmless
-- and is not: a test that inserts a second owner cannot tell which index
-- refused it, so the new one would sit unproven until the old one is removed —
-- and that is precisely when nobody is looking. Dropping an index removes no
-- data and breaks no query.
--
-- Not CONCURRENTLY, and the reason is the same one written above the index in
-- 00008: it cannot run inside a transaction, so a failure cannot be rolled
-- back and leaves the migration wedged. The lock it would avoid is already
-- held anyway — ALTER TABLE users ADD COLUMN above takes ACCESS EXCLUSIVE on
-- this same table in this same transaction, so the drop costs nothing extra.
-- squawk-ignore require-concurrent-index-deletion
DROP INDEX users_single_owner_key;

-- The view froze its column list when it was created, so role does not appear
-- on its own — and a _live view that quietly stops showing a column is how a
-- soft-delete leak begins. role goes last because CREATE OR REPLACE VIEW may
-- only append.
CREATE OR REPLACE VIEW users_live AS
SELECT id,
       email,
       name,
       password_hash,
       timezone,
       is_owner,
       created_at,
       updated_at,
       deactivated_at,
       deleted_at,
       role
FROM users
WHERE deleted_at IS NULL;

-- Membership becomes binary: someone is a member, or is not. What they may do
-- once inside comes from users.role.
--
-- ban-drop-column is ignored twice below. The rule exists because a dropped
-- column breaks code that still selects it, and because the data is gone for
-- good. Neither applies: no query in this repository reads either column —
-- project_members and folder_members have no sqlc queries at all yet — and
-- both tables are empty in every installation, since nothing can create a
-- project through the API today.
-- squawk-ignore ban-drop-column
ALTER TABLE project_members DROP COLUMN role;
-- squawk-ignore ban-drop-column
ALTER TABLE folder_members DROP COLUMN role;

-- invitations.role now carries the ACCOUNT role the invitee will be created
-- with, not a role inside a project. Invitations exist only to create an
-- employee's account; adding somebody who already has one to a project takes
-- effect immediately and needs no invitation at all.
--
-- 'owner' is absent on purpose. There is exactly one, and that account is the
-- first one, not an invited one.
ALTER TABLE invitations DROP CONSTRAINT invitations_role_check;

ALTER TABLE invitations
    ADD CONSTRAINT invitations_role_known
    CHECK (role IN ('maintainer', 'contributor', 'viewer')) NOT VALID;

ALTER TABLE invitations VALIDATE CONSTRAINT invitations_role_known;
