-- Invitations. Like sessions, the token itself never reaches this file: what is
-- stored and what is looked up is its SHA-256, computed in the domain layer
-- (ADR-0005). And like sessions, token_hash is never selected — nothing outside
-- redemption may see it, and one stray column in a list is a credential sitting
-- in a JSON body.
--
-- invitations.role holds the ACCOUNT role the invitee will be created with
-- (ADR-0012). The CHECK on the column accepts only maintainer, contributor, and
-- viewer: there is exactly one owner and that account is the first one, not an
-- invited one.

-- name: CreateInvitation :one
INSERT INTO invitations (email, token_hash, invited_by, role, expires_at)
VALUES (
    sqlc.arg(email)::text,
    sqlc.arg(token_hash)::bytea,
    sqlc.arg(invited_by),
    sqlc.arg(role)::text,
    sqlc.arg(expires_at)::timestamptz
)
RETURNING id, email, role, invited_by, created_at, expires_at, accepted_at;

-- Redemption. Expiry and acceptance are deliberately not filtered here, for the
-- same reason GetSessionByTokenHash does not filter expiry: the caller has to
-- tell an expired link from one that never existed for the log, and a WHERE
-- clause that hides the row makes the two indistinguishable. What the caller
-- must not do is let that difference reach the client — see Invitation.IsUsable.
-- name: GetInvitationByTokenHash :one
SELECT id, email, role, invited_by, created_at, expires_at, accepted_at
FROM invitations
WHERE token_hash = sqlc.arg(token_hash)::bytea;

-- Stamping the invitation as used, and the only thing that makes it single-use.
--
-- The conditions are in the WHERE rather than checked in Go beforehand. Two
-- clicks on the same link arriving together would both read an open invitation
-- and both go on to create an account; here the second one updates no rows and
-- the caller can see that it lost. The account is created in the same
-- transaction as this statement, so a redemption that loses creates nothing.
-- name: AcceptInvitation :execrows
UPDATE invitations
SET accepted_at = now()
WHERE id = sqlc.arg(id)::uuid
  AND accepted_at IS NULL
  AND expires_at > now();

-- Re-inviting somebody. The previous link stops working the moment a new one is
-- sent, rather than two links for one address both staying live -- an address
-- that was invited twice by mistake should not end up with a spare account
-- creator sitting in an old e-mail.
--
-- lower(email) matches invitations_email_idx, which is partial on
-- accepted_at IS NULL and exists for exactly this lookup. Accepted rows are left
-- alone: they are the record that an account was created from this invitation,
-- and rewriting their deadline would rewrite that history.
-- name: ExpireOpenInvitationsForEmail :execrows
UPDATE invitations
SET expires_at = now()
WHERE lower(email) = lower(sqlc.arg(email)::text)
  AND accepted_at IS NULL
  AND expires_at > now();
