-- Password resets. Like sessions and invitations, the token itself never
-- reaches this file: what is stored and what is looked up is its SHA-256,
-- computed in the domain layer (ADR-0005). And like both of them, token_hash is
-- never selected -- nothing outside confirmation may see it, and one stray
-- column in a row is a credential on its way into a JSON body.
--
-- There is no address anywhere here. The row points at an account by id, so a
-- link requested before somebody changed their address cannot resurrect the old
-- one.

-- name: CreatePasswordReset :one
INSERT INTO password_resets (user_id, token_hash, expires_at)
VALUES (
    sqlc.arg(user_id),
    sqlc.arg(token_hash)::bytea,
    sqlc.arg(expires_at)::timestamptz
)
RETURNING id, user_id, created_at, expires_at, used_at;

-- Confirmation. Expiry and use are deliberately not filtered here, for the same
-- reason GetInvitationByTokenHash does not filter them: the caller has to tell a
-- spent link from one that never existed for the log, and a WHERE clause that
-- hides the row makes the two indistinguishable. What the caller must not do is
-- let that difference reach the client -- see PasswordReset.IsUsable.
-- name: GetPasswordResetByTokenHash :one
SELECT id, user_id, created_at, expires_at, used_at
FROM password_resets
WHERE token_hash = sqlc.arg(token_hash)::bytea;

-- Stamping the reset as spent, and the only thing that makes it single-use.
--
-- The conditions are in the WHERE rather than checked in Go beforehand. Two
-- confirmations of the same link arriving together would both read an open row
-- and both go on to write a password; here the second one updates no rows and
-- the caller can see that it lost. The password is written in the same
-- transaction as this statement, so a confirmation that loses changes nothing --
-- which matters more than it does for invitations, because the loser would
-- otherwise set the password of an account that is already somebody's.
-- name: UsePasswordReset :execrows
UPDATE password_resets
SET used_at = now()
WHERE id = sqlc.arg(id)::uuid
  AND used_at IS NULL
  AND expires_at > now();

-- Asking for a second link. The previous one stops working the moment a new one
-- is sent, rather than every link ever requested staying live until its own
-- deadline -- somebody who clicks "forgot password" four times should not leave
-- four ways into their account lying in an inbox.
--
-- Spent rows are left alone: they are the record that a password was replaced
-- through them, and rewriting their deadline would rewrite that history.
-- name: ExpireOpenPasswordResetsForUser :execrows
UPDATE password_resets
SET expires_at = now()
WHERE user_id = sqlc.arg(user_id)
  AND used_at IS NULL
  AND expires_at > now();
