-- +goose Up

SET lock_timeout = '5s';
SET statement_timeout = '5min';

-- The **contract** half of expand-migrate-contract, closing what 00009 opened.
--
-- 00009 added users.role beside is_owner and a constraint that refused to let
-- them disagree. The identity module has since moved to role — its sqlc
-- queries, its structs, its handlers, and the API contract all read it — so
-- the older column now has no readers left and can go.
--
-- The order below is not interchangeable: users_live selects is_owner, and
-- PostgreSQL refuses to drop a column a view depends on. So the view goes
-- first, then the column, then the view comes back without it.

-- CREATE OR REPLACE VIEW can only append columns, never remove one, so this is
-- a drop and a create rather than a replace.
DROP VIEW users_live;

-- Its whole purpose was to keep the two columns honest while both existed.
-- With one of them gone it would refuse every row.
ALTER TABLE users DROP CONSTRAINT users_owner_columns_agree;

-- ban-drop-column is ignored, and this is the case the rule's exception was
-- written for: the column has no readers. Nothing in this repository selects
-- is_owner any more (PR #61 moved the identity module to role), which is
-- precisely the state expand-migrate-contract exists to reach before the
-- contract step runs. Dropping it earlier would have broken every query in
-- main; dropping it later would leave two answers to "who is the owner"
-- waiting to disagree.
-- squawk-ignore ban-drop-column
ALTER TABLE users DROP COLUMN is_owner;

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
       role,
       created_at,
       updated_at,
       deactivated_at,
       deleted_at
FROM users
WHERE deleted_at IS NULL;
