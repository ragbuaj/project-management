-- Every read goes through users_live, never through users. The view is where
-- soft delete is enforced (docs/data-model.md); a query that names the table
-- directly is one forgotten WHERE away from returning deleted accounts.
--
-- The two exceptions are the writes below, which have to name the table
-- because a view is not what you insert into or soft-delete through.

-- name: GetUserByID :one
SELECT id, email, name, password_hash, timezone, role, created_at, updated_at
FROM users_live
WHERE id = $1;

-- Login. Compared with lower() because addresses are not case sensitive and
-- users_email_key indexes lower(email) for exactly this lookup.
-- name: GetUserByEmail :one
SELECT id, email, name, password_hash, timezone, role, created_at, updated_at
FROM users_live
WHERE lower(email) = lower(sqlc.arg(email)::text);

-- name: ListUsers :many
SELECT id, email, name, role, created_at
FROM users_live
ORDER BY lower(name);

-- The account role is chosen by whoever creates the account, never defaulted
-- here: users.role has a DEFAULT, and relying on it would mean a caller that
-- forgets to decide silently gets one (ADR-0012).
-- name: CreateUser :one
INSERT INTO users (email, name, password_hash, role)
VALUES (
    sqlc.arg(email)::text,
    sqlc.arg(name)::text,
    sqlc.arg(password_hash)::text,
    sqlc.arg(role)::text
)
RETURNING id, email, name, role, created_at;

-- Masking, not deleting: the account keeps its rows so that its cards and
-- comments survive with an author (docs/data-model.md, retention). The address
-- is replaced rather than blanked so the partial unique index frees the
-- original for reuse.
-- name: SoftDeleteUser :execrows
UPDATE users
SET deleted_at = now(),
    email = 'deleted+' || id::text || '@invalid',
    name = 'Pengguna dihapus',
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND deleted_at IS NULL;

-- Rewriting a hash at a cost the application has since raised. A successful
-- login is the only moment this is possible, because it is the only moment the
-- password is in memory.
--
-- The old hash is matched as well as the id, so two logins racing each other
-- cannot have one overwrite the other's rehash with a stale value.
-- name: UpdateUserPasswordHash :execrows
UPDATE users
SET password_hash = sqlc.arg(new_hash)::text,
    updated_at = now()
WHERE id = sqlc.arg(id)
  AND password_hash = sqlc.arg(current_hash)::text
  AND deleted_at IS NULL;
