package repository_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

func at(t time.Time) pgtype.Timestamptz {
	return pgtype.Timestamptz{Time: t, Valid: true}
}

// digest stands in for what the domain layer computes. Only its width matters
// to these queries -- invitations_token_hash_key is unique and the column has a
// CHECK on octet_length -- so a recognizable filler is more useful here than a
// real SHA-256 nobody can match up in a failure message.
func digest(seed byte) []byte {
	d := make([]byte, 32)
	for i := range d {
		d[i] = seed
	}

	return d
}

func inviteFrom(t *testing.T, ctx context.Context, q *identityrepo.Queries, inviter, email string, seed byte, expires time.Time) identityrepo.CreateInvitationRow {
	t.Helper()

	row, err := q.CreateInvitation(ctx, identityrepo.CreateInvitationParams{
		Email:     email,
		TokenHash: digest(seed),
		InvitedBy: inviter,
		Role:      "contributor",
		ExpiresAt: at(expires),
	})
	if err != nil {
		t.Fatalf("CreateInvitation(%s): %v", email, err)
	}

	return row
}

func TestAnInvitationIsFoundByTheDigestOfItsLink(t *testing.T) {
	ctx, _, q := queriesTx(t)

	inviter := createUser(t, ctx, q, "owner@example.test", "Owner")
	deadline := time.Now().Add(7 * 24 * time.Hour)

	created := inviteFrom(t, ctx, q, inviter, "budi@example.test", 1, deadline)

	if created.AcceptedAt.Valid {
		t.Error("a fresh invitation already carries an accepted_at")
	}

	found, err := q.GetInvitationByTokenHash(ctx, digest(1))
	if err != nil {
		t.Fatalf("GetInvitationByTokenHash(): %v", err)
	}

	if found.ID != created.ID {
		t.Errorf("found invitation %s, want %s", found.ID, created.ID)
	}

	if found.Email != "budi@example.test" || found.Role != "contributor" || found.InvitedBy != inviter {
		t.Errorf("found %+v, want the invitation as it was written", found)
	}
}

// The same rule sessions live under: nothing outside redemption may see the
// digest, and one stray column is a credential sitting in a JSON body. Checked
// by reflection rather than by reading the SQL, because the SQL is what would
// change.
func TestNoInvitationRowCarriesTheTokenHash(t *testing.T) {
	t.Parallel()

	for _, row := range []any{
		identityrepo.CreateInvitationRow{},
		identityrepo.GetInvitationByTokenHashRow{},
	} {
		typ := reflect.TypeOf(row)

		for i := range typ.NumField() {
			if name := typ.Field(i).Name; strings.Contains(strings.ToLower(name), "token") {
				t.Errorf("%s has a field %q", typ.Name(), name)
			}
		}
	}
}

func TestAnInvitationIsAcceptedOnlyOnce(t *testing.T) {
	ctx, _, q := queriesTx(t)

	inviter := createUser(t, ctx, q, "owner@example.test", "Owner")
	created := inviteFrom(t, ctx, q, inviter, "budi@example.test", 2, time.Now().Add(time.Hour))

	rows, err := q.AcceptInvitation(ctx, created.ID)
	if err != nil {
		t.Fatalf("AcceptInvitation(): %v", err)
	}

	if rows != 1 {
		t.Fatalf("the first acceptance changed %d rows, want 1", rows)
	}

	rows, err = q.AcceptInvitation(ctx, created.ID)
	if err != nil {
		t.Fatalf("second AcceptInvitation(): %v", err)
	}

	if rows != 0 {
		t.Errorf("the second acceptance changed %d rows, want 0 — the link is reusable", rows)
	}
}

func TestAnExpiredInvitationCannotBeAccepted(t *testing.T) {
	ctx, _, q := queriesTx(t)

	inviter := createUser(t, ctx, q, "owner@example.test", "Owner")
	created := inviteFrom(t, ctx, q, inviter, "late@example.test", 3, time.Now().Add(-time.Minute))

	rows, err := q.AcceptInvitation(ctx, created.ID)
	if err != nil {
		t.Fatalf("AcceptInvitation(): %v", err)
	}

	if rows != 0 {
		t.Errorf("an expired invitation was accepted, changing %d rows", rows)
	}
}

// Re-inviting has to close the previous link, or an address invited twice by
// mistake leaves a spare account creator sitting in an old e-mail.
func TestReInvitingClosesTheLinkAlreadySent(t *testing.T) {
	ctx, _, q := queriesTx(t)

	inviter := createUser(t, ctx, q, "owner@example.test", "Owner")
	first := inviteFrom(t, ctx, q, inviter, "budi@example.test", 4, time.Now().Add(time.Hour))

	// Spelled differently on purpose: addresses are not case sensitive, and the
	// partial index this statement leans on is on lower(email).
	closed, err := q.ExpireOpenInvitationsForEmail(ctx, "BUDI@Example.test")
	if err != nil {
		t.Fatalf("ExpireOpenInvitationsForEmail(): %v", err)
	}

	if closed != 1 {
		t.Fatalf("closed %d open invitations, want 1", closed)
	}

	rows, err := q.AcceptInvitation(ctx, first.ID)
	if err != nil {
		t.Fatalf("AcceptInvitation(): %v", err)
	}

	if rows != 0 {
		t.Errorf("the superseded link still accepted, changing %d rows", rows)
	}
}

// An accepted invitation is the record that an account was created from it.
// Rewriting its deadline would rewrite that history.
func TestSupersedingLeavesAcceptedInvitationsAlone(t *testing.T) {
	ctx, _, q := queriesTx(t)

	inviter := createUser(t, ctx, q, "owner@example.test", "Owner")
	deadline := time.Now().Add(time.Hour)
	accepted := inviteFrom(t, ctx, q, inviter, "budi@example.test", 5, deadline)

	if _, err := q.AcceptInvitation(ctx, accepted.ID); err != nil {
		t.Fatalf("AcceptInvitation(): %v", err)
	}

	closed, err := q.ExpireOpenInvitationsForEmail(ctx, "budi@example.test")
	if err != nil {
		t.Fatalf("ExpireOpenInvitationsForEmail(): %v", err)
	}

	if closed != 0 {
		t.Errorf("superseding touched %d accepted invitations, want 0", closed)
	}

	found, err := q.GetInvitationByTokenHash(ctx, digest(5))
	if err != nil {
		t.Fatalf("GetInvitationByTokenHash(): %v", err)
	}

	if !found.ExpiresAt.Time.Equal(deadline.UTC().Truncate(time.Microsecond)) &&
		!found.ExpiresAt.Time.Equal(deadline) {
		t.Errorf("the accepted invitation's deadline moved to %v, want %v", found.ExpiresAt.Time, deadline)
	}

	if !found.AcceptedAt.Valid {
		t.Error("the accepted invitation lost its accepted_at")
	}
}

// `owner` is refused by the schema, not by the application: there is exactly
// one owner and that account is the first one, not an invited one. Checked here
// because a CHECK constraint nobody tests is a CHECK somebody drops.
func TestNobodyCanBeInvitedAsOwner(t *testing.T) {
	ctx, _, q := queriesTx(t)

	inviter := createUser(t, ctx, q, "owner@example.test", "Owner")

	_, err := q.CreateInvitation(ctx, identityrepo.CreateInvitationParams{
		Email:     "second@example.test",
		TokenHash: digest(6),
		InvitedBy: inviter,
		Role:      "owner",
		ExpiresAt: at(time.Now().Add(time.Hour)),
	})
	if err == nil {
		t.Fatal("an invitation to become owner was accepted by the database")
	}

	if !strings.Contains(err.Error(), "invitations_role_known") {
		t.Errorf("refused by %v, want the invitations_role_known constraint", err)
	}
}

// The claim AcceptInvitation exists for, tested the only way it can be: two
// redemptions that really do run at once, in separate transactions on separate
// connections. Run sequentially this passes even with the guard removed --
// which is exactly how a test comes to look like it is testing something.
func TestConcurrentRedemptionsLetExactlyOneThrough(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; start compose or run this in CI")
	}

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	pool, err := postgres.New(t.Context(), url, 10)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	t.Cleanup(cancel)

	// This test commits, because two transactions cannot race inside one. What
	// it writes is removed again on the way out.
	q := identityrepo.New(pool)

	inviter, err := q.CreateUser(ctx, identityrepo.CreateUserParams{
		Email:        "race-owner@example.test",
		Name:         "Owner",
		PasswordHash: "argon2id$placeholder",
		Role:         "owner",
	})
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM invitations WHERE invited_by = $1`, inviter.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, inviter.ID)
	})

	invitation := inviteFrom(t, ctx, q, inviter.ID, "race@example.test", 7, time.Now().Add(time.Hour))

	const redeemers = 20

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		won    int
		start  = make(chan struct{})
		errsCh = make(chan error, redeemers)
	)

	for range redeemers {
		wg.Go(func() {
			<-start

			tx, err := pool.Begin(ctx)
			if err != nil {
				errsCh <- err

				return
			}

			defer func() { _ = tx.Rollback(ctx) }()

			rows, err := identityrepo.New(tx).AcceptInvitation(ctx, invitation.ID)
			if err != nil {
				errsCh <- err

				return
			}

			if err := tx.Commit(ctx); err != nil {
				errsCh <- err

				return
			}

			mu.Lock()
			won += int(rows)
			mu.Unlock()
		})
	}

	close(start)
	wg.Wait()
	close(errsCh)

	for err := range errsCh {
		t.Errorf("redeemer: %v", err)
	}

	if won != 1 {
		t.Errorf("%d of %d redemptions were accepted, want exactly 1 — each one would create an account", won, redeemers)
	}
}
