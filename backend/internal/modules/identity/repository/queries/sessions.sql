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
