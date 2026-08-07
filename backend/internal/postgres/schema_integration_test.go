package postgres_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// hash stands in for the SHA-256 of a token. Only its length matters to the
// schema, which is the whole point of storing a digest.
func hash(b byte) []byte {
	h := make([]byte, 32)
	h[0] = b

	return h
}

// seedConnection writes a VCS connection into the seeded project.
func seedConnection(t *testing.T, ctx context.Context, tx pgx.Tx, p project) string {
	t.Helper()

	var id string

	err := tx.QueryRow(ctx, `
		INSERT INTO vcs_connections (project_id, provider, repo_full_name, credential_enc, webhook_secret_enc)
		VALUES ($1, 'github', 'ragbuaj/project-management', $2, $3)
		RETURNING id`, p.id, hash(1), hash(2)).Scan(&id)
	if err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	return id
}

// A digest that is not 32 bytes is not a SHA-256 of anything. It is the token
// itself, or a truncation, or a hex string somebody forgot to decode — and all
// three are how a credential ends up readable in the database.
func TestATokenHashHasToBeADigest(t *testing.T) {
	ctx, tx, p := projectTx(t)

	user := reporterOf(t, ctx, tx, p)

	const insert = `
		INSERT INTO api_tokens (user_id, name, token_hash, scopes, expires_at)
		VALUES ($1, 'CI', $2, ARRAY['cards:read'], now() + interval '30 days')`

	for _, bad := range [][]byte{{}, make([]byte, 16), make([]byte, 64)} {
		if err := attempt(t, ctx, tx, insert, user, bad); err == nil {
			t.Errorf("a %d-byte token hash was accepted", len(bad))
		}
	}

	if err := attempt(t, ctx, tx, insert, user, hash(1)); err != nil {
		t.Errorf("a 32-byte digest was rejected: %v", err)
	}
}

// Two tokens hashing to one value would make the lookup return whichever row
// came first, authenticating one account with another's credential. A token
// with no scopes can do nothing and is therefore a row written by mistake.
func TestAPITokensAreUniqueAndScoped(t *testing.T) {
	ctx, tx, p := projectTx(t)

	user := reporterOf(t, ctx, tx, p)

	const insert = `
		INSERT INTO api_tokens (user_id, name, token_hash, scopes, expires_at)
		VALUES ($1, $2, $3, $4, now() + interval '30 days')`

	if err := attempt(t, ctx, tx, insert, user, "CI", hash(1), []string{"cards:read"}); err != nil {
		t.Fatalf("first token: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, user, "Other", hash(1), []string{"cards:read"}); err == nil {
		t.Error("two tokens share one hash")
	}

	if err := attempt(t, ctx, tx, insert, user, "Empty", hash(2), []string{}); err == nil {
		t.Error("a token with no scopes was accepted")
	}
}

// Revoking a board's share link and deleting the board are different things,
// and the second must not be needed to achieve the first. Deleting the board,
// however, must take the link with it — a live token pointing at nothing is a
// route into an error page at best.
func TestDeletingABoardRevokesItsShareLinks(t *testing.T) {
	ctx, tx, p := projectTx(t)

	creator := reporterOf(t, ctx, tx, p)

	if _, err := tx.Exec(ctx, `
		INSERT INTO share_links (board_id, token_hash, created_by)
		VALUES ($1, $2, $3)`, p.board, hash(1), creator); err != nil {
		t.Fatalf("share link: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM boards WHERE id = $1`, p.board); err != nil {
		t.Fatalf("delete board: %v", err)
	}

	var left int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM share_links WHERE board_id = $1`, p.board).Scan(&left); err != nil {
		t.Fatalf("count links: %v", err)
	}

	if left != 0 {
		t.Errorf("%d share links outlived their board", left)
	}
}

// A share link may be permanent — unlike an API token, it is read-only and
// scoped to one board, and revocation is the control that matters. This
// records the difference so neither column drifts into the other's shape.
func TestAShareLinkMayHaveNoExpiryButATokenMayNot(t *testing.T) {
	ctx, tx, p := projectTx(t)

	user := reporterOf(t, ctx, tx, p)

	err := attempt(t, ctx, tx, `
		INSERT INTO share_links (board_id, token_hash, created_by, expires_at)
		VALUES ($1, $2, $3, NULL)`, p.board, hash(1), user)
	if err != nil {
		t.Errorf("a share link without an expiry was rejected: %v", err)
	}

	err = attempt(t, ctx, tx, `
		INSERT INTO api_tokens (user_id, name, token_hash, scopes, expires_at)
		VALUES ($1, 'Forever', $2, ARRAY['cards:read'], NULL)`, user, hash(2))
	if err == nil {
		t.Error("an API token without an expiry was accepted")
	}
}

// Providers redeliver whenever they are unsure the response arrived, so the
// same push comes in more than once as a matter of course. Without the unique
// key the second copy creates a second set of links on the card.
func TestAWebhookDeliveryIsRecordedOnce(t *testing.T) {
	ctx, tx, p := projectTx(t)

	connection := seedConnection(t, ctx, tx, p)

	const insert = `
		INSERT INTO vcs_webhook_deliveries (connection_id, delivery_id, raw_body)
		VALUES ($1, $2, '{}'::jsonb)`

	if err := attempt(t, ctx, tx, insert, connection, "delivery-1"); err != nil {
		t.Fatalf("first delivery: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, connection, "delivery-1"); err == nil {
		t.Error("the same delivery was recorded twice")
	}

	if err := attempt(t, ctx, tx, insert, connection, "delivery-2"); err != nil {
		t.Errorf("a different delivery was rejected: %v", err)
	}
}

// One merge request may legitimately close several cards, which is why the
// card is part of the key. The same link on the same card is still a
// duplicate.
func TestOneChangeRequestCanLinkSeveralCards(t *testing.T) {
	ctx, tx, p := projectTx(t)

	connection := seedConnection(t, ctx, tx, p)
	first := seedCard(t, ctx, tx, p, 1, "a0")
	second := seedCard(t, ctx, tx, p, 2, "a1")

	const insert = `
		INSERT INTO vcs_links (card_id, connection_id, kind, external_id, url)
		VALUES ($1, $2, 'change_request', '42', $3)`

	const url = "https://github.com/ragbuaj/project-management/pull/42"

	if err := attempt(t, ctx, tx, insert, first, connection, url); err != nil {
		t.Fatalf("first link: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, second, connection, url); err != nil {
		t.Errorf("the same change request could not link a second card: %v", err)
	}

	if err := attempt(t, ctx, tx, insert, first, connection, url); err == nil {
		t.Error("the same link was stored twice on one card")
	}
}

// A link is followed by a person clicking it. An http:// or javascript: value
// arriving from a provider response is not something to render into an anchor.
func TestAVCSLinkURLHasToBeHTTPS(t *testing.T) {
	ctx, tx, p := projectTx(t)

	connection := seedConnection(t, ctx, tx, p)
	card := seedCard(t, ctx, tx, p, 1, "a0")

	const insert = `
		INSERT INTO vcs_links (card_id, connection_id, kind, external_id, url)
		VALUES ($1, $2, 'issue', $3, $4)`

	for i, bad := range []string{
		"http://github.com/x/y/issues/1",
		"javascript:alert(1)",
		"/relative/path",
	} {
		if err := attempt(t, ctx, tx, insert, card, connection, i, bad); err == nil {
			t.Errorf("url %q was accepted", bad)
		}
	}

	err := attempt(t, ctx, tx, insert, card, connection, "9",
		"https://github.com/ragbuaj/project-management/issues/9")
	if err != nil {
		t.Errorf("an https url was rejected: %v", err)
	}
}

// A second connection to the same repository would double every webhook and
// every sync, and the repo name has to look like one — "owner/name" is what
// every provider path is built from.
func TestOneConnectionPerRepository(t *testing.T) {
	ctx, tx, p := projectTx(t)

	seedConnection(t, ctx, tx, p)

	const insert = `
		INSERT INTO vcs_connections (project_id, provider, repo_full_name, credential_enc, webhook_secret_enc)
		VALUES ($1, 'github', $2, $3, $4)`

	err := attempt(t, ctx, tx, insert, p.id, "ragbuaj/project-management", hash(3), hash(4))
	if err == nil {
		t.Error("the same repository was connected twice")
	}

	for _, bad := range []string{"project-management", "a/b/c", "owner /name"} {
		if err := attempt(t, ctx, tx, insert, p.id, bad, hash(5), hash(6)); err == nil {
			t.Errorf("repo name %q was accepted", bad)
		}
	}

	err = attempt(t, ctx, tx, insert, p.id, "ragbuaj/other", hash(7), hash(8))
	if err != nil {
		t.Errorf("a second repository was rejected: %v", err)
	}
}

// Deleting a project takes its connection, and the connection takes its links
// and its delivery log. Credentials left behind after a project is gone are
// the worst kind of orphan.
func TestDeletingAProjectRemovesItsVCSCredentials(t *testing.T) {
	ctx, tx, p := projectTx(t)

	connection := seedConnection(t, ctx, tx, p)
	card := seedCard(t, ctx, tx, p, 1, "a0")

	if _, err := tx.Exec(ctx, `
		INSERT INTO vcs_links (card_id, connection_id, kind, external_id, url)
		VALUES ($1, $2, 'branch', 'feat/x', 'https://github.com/a/b/tree/feat/x')`,
		card, connection); err != nil {
		t.Fatalf("link: %v", err)
	}

	if _, err := tx.Exec(ctx, `
		INSERT INTO vcs_webhook_deliveries (connection_id, delivery_id, raw_body)
		VALUES ($1, 'delivery-1', '{}'::jsonb)`, connection); err != nil {
		t.Fatalf("delivery: %v", err)
	}

	if _, err := tx.Exec(ctx, `DELETE FROM projects WHERE id = $1`, p.id); err != nil {
		t.Fatalf("delete project: %v", err)
	}

	for _, table := range []string{"vcs_links", "vcs_webhook_deliveries"} {
		var left int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM `+table+` WHERE connection_id = $1`, connection).Scan(&left); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}

		if left != 0 {
			t.Errorf("%d rows left in %s after the project was deleted", left, table)
		}
	}

	var connections int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM vcs_connections WHERE id = $1`, connection).Scan(&connections); err != nil {
		t.Fatalf("count connections: %v", err)
	}

	if connections != 0 {
		t.Error("the encrypted credentials outlived their project")
	}
}

// Expiry is what forces rotation, so the column has to actually be readable as
// a deadline rather than as a creation stamp with a different name.
func TestAnExpiredTokenIsStillDistinguishable(t *testing.T) {
	ctx, tx, p := projectTx(t)

	user := reporterOf(t, ctx, tx, p)

	if _, err := tx.Exec(ctx, `
		INSERT INTO api_tokens (user_id, name, token_hash, scopes, expires_at)
		VALUES ($1, 'Old', $2, ARRAY['cards:read'], $3)`,
		user, hash(1), time.Now().Add(-time.Hour)); err != nil {
		t.Fatalf("expired token: %v", err)
	}

	var live int

	err := tx.QueryRow(ctx, `
		SELECT count(*) FROM api_tokens
		WHERE user_id = $1 AND revoked_at IS NULL AND expires_at > now()`, user).Scan(&live)
	if err != nil {
		t.Fatalf("count live tokens: %v", err)
	}

	if live != 0 {
		t.Errorf("an expired token still counts as live (%d)", live)
	}
}
