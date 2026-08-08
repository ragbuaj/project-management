-- Sessions. The token itself never reaches this file: what is stored and what
-- is looked up is its SHA-256, computed in the domain layer (ADR-0005).

-- name: CreateSession :one
INSERT INTO sessions (user_id, token_hash, user_agent, expires_at)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(token_hash)::bytea,
    sqlc.arg(user_agent)::text,
    sqlc.arg(expires_at)::timestamptz
)
RETURNING id, user_id, created_at, last_seen_at, expires_at;

-- Every authenticated request runs this, which is why it also returns the
-- user: the alternative is two round trips per request for data that is
-- always needed together.
--
-- The join is against users_live, so a soft-deleted account stops
-- authenticating the moment it is masked — without a sweep having to reach its
-- sessions first. A join against users would keep them working until then.
--
-- Expiry is deliberately not filtered here. The caller has to distinguish an
-- expired session from one that never existed for the log, and a WHERE clause
-- that hides the row makes the two indistinguishable.
-- name: GetSessionByTokenHash :one
SELECT s.id,
       s.user_id,
       s.created_at,
       s.last_seen_at,
       s.expires_at,
       u.email,
       u.name,
       u.timezone,
       u.role
FROM sessions s
JOIN users_live u ON u.id = s.user_id
WHERE s.token_hash = sqlc.arg(token_hash)::bytea;

-- Sliding renewal. The caller decides how often this is worth running; see
-- Session.NeedsRenewal, which exists so this is not one write per request.
-- name: TouchSession :execrows
UPDATE sessions
SET last_seen_at = now(),
    expires_at = sqlc.arg(expires_at)::timestamptz
WHERE id = sqlc.arg(id);

-- Logout. ADR-0005 chose opaque sessions precisely so this is all it takes:
-- one DELETE, effective on the next request, with no revocation list.
-- name: DeleteSessionByTokenHash :execrows
DELETE FROM sessions
WHERE token_hash = sqlc.arg(token_hash)::bytea;

-- The account settings screen: what is signed in right now. The token hash is
-- never selected. Nothing outside authentication may see it, and a list
-- endpoint is exactly the place where one stray column becomes a credential
-- sitting in a JSON body.
--
-- Expired rows are filtered rather than listed as dead. The question the screen
-- answers is "is anything signed in that should not be", and a row that can no
-- longer authenticate is not an answer to it. Removing them is a job (Langkah
-- 24); this only decides what is shown.
-- name: ListSessionsByUser :many
SELECT id, user_agent, created_at, last_seen_at, expires_at
FROM sessions
WHERE user_id = sqlc.arg(user_id)
  AND expires_at > now()
ORDER BY last_seen_at DESC, id;

-- Revoking one session. user_id is in the WHERE clause rather than checked in
-- Go after the row comes back: a session that is not the caller's must not be
-- deletable, and the only way to be sure of that is for the statement itself to
-- be unable to reach it. docs/authorization.md keeps sessions to their owner
-- even for `owner`.
-- name: DeleteSessionForUser :execrows
DELETE FROM sessions
WHERE id = sqlc.arg(id)::uuid
  AND user_id = sqlc.arg(user_id);

-- Signing out everywhere else. The session making the request is kept, because
-- the caller asked to end the others and an answer they cannot receive while
-- still signed in is not the thing they asked for.
-- name: DeleteOtherSessionsForUser :execrows
DELETE FROM sessions
WHERE user_id = sqlc.arg(user_id)
  AND id <> sqlc.arg(current_id)::uuid;

-- Signing out everywhere, with no exception -- the one above keeps the caller's
-- session because they asked from inside it, and this one has no caller to keep.
--
-- It exists for password reset, where whoever is signing in is not necessarily
-- whoever was already signed in. A reset that left the old sessions alive would
-- leave the account in the hands it was being taken out of, which is the one
-- thing somebody clicking "forgot password" after a scare is trying to undo.
-- name: DeleteAllSessionsForUser :execrows
DELETE FROM sessions
WHERE user_id = sqlc.arg(user_id);
