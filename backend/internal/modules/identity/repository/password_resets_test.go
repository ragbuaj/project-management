package repository_test

import (
	"context"
	"os"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

func resetFor(t *testing.T, ctx context.Context, q *identityrepo.Queries, user string, seed byte, expires time.Time) identityrepo.CreatePasswordResetRow {
	t.Helper()

	row, err := q.CreatePasswordReset(ctx, identityrepo.CreatePasswordResetParams{
		UserID:    user,
		TokenHash: digest(seed),
		ExpiresAt: at(expires),
	})
	if err != nil {
		t.Fatalf("CreatePasswordReset(): %v", err)
	}

	return row
}

func TestAPasswordResetIsFoundByTheDigestOfItsLink(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "budi@example.test", "Budi")
	// An hour rather than identitydom.PasswordResetWindow: how long a link lives
	// is policy, and these statements are indifferent to it. Reaching for the
	// constant would tie the schema's tests to a number that is allowed to move.
	created := resetFor(t, ctx, q, user, 20, time.Now().Add(time.Hour))

	if created.UsedAt.Valid {
		t.Error("a fresh reset already carries a used_at")
	}

	found, err := q.GetPasswordResetByTokenHash(ctx, digest(20))
	if err != nil {
		t.Fatalf("GetPasswordResetByTokenHash(): %v", err)
	}

	if found.ID != created.ID || found.UserID != user {
		t.Errorf("found %+v, want the reset as it was written for %s", found, user)
	}
}

// The same rule sessions and invitations live under. Checked by reflection
// rather than by reading the SQL, because the SQL is what would change.
func TestNoPasswordResetRowCarriesTheTokenHash(t *testing.T) {
	t.Parallel()

	for _, row := range []any{
		identityrepo.CreatePasswordResetRow{},
		identityrepo.GetPasswordResetByTokenHashRow{},
	} {
		typ := reflect.TypeOf(row)

		for i := range typ.NumField() {
			if name := typ.Field(i).Name; strings.Contains(strings.ToLower(name), "token") {
				t.Errorf("%s has a field %q", typ.Name(), name)
			}
		}
	}
}

// A reset row carries no address either. The link points at an account by id, so
// one requested before somebody changed their address cannot resurrect the old
// one.
func TestNoPasswordResetRowCarriesAnAddress(t *testing.T) {
	t.Parallel()

	for _, row := range []any{
		identityrepo.CreatePasswordResetRow{},
		identityrepo.GetPasswordResetByTokenHashRow{},
	} {
		typ := reflect.TypeOf(row)

		for i := range typ.NumField() {
			if name := typ.Field(i).Name; strings.Contains(strings.ToLower(name), "email") {
				t.Errorf("%s has a field %q", typ.Name(), name)
			}
		}
	}
}

func TestAPasswordResetIsUsedOnlyOnce(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "budi@example.test", "Budi")
	created := resetFor(t, ctx, q, user, 21, time.Now().Add(time.Hour))

	rows, err := q.UsePasswordReset(ctx, created.ID)
	if err != nil {
		t.Fatalf("UsePasswordReset(): %v", err)
	}

	if rows != 1 {
		t.Fatalf("the first use changed %d rows, want 1", rows)
	}

	rows, err = q.UsePasswordReset(ctx, created.ID)
	if err != nil {
		t.Fatalf("second UsePasswordReset(): %v", err)
	}

	if rows != 0 {
		t.Errorf("the second use changed %d rows, want 0 — the link sets a password twice", rows)
	}
}

func TestAnExpiredPasswordResetCannotBeUsed(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "late@example.test", "Late")
	created := resetFor(t, ctx, q, user, 22, time.Now().Add(-time.Minute))

	rows, err := q.UsePasswordReset(ctx, created.ID)
	if err != nil {
		t.Fatalf("UsePasswordReset(): %v", err)
	}

	if rows != 0 {
		t.Errorf("an expired reset was used, changing %d rows", rows)
	}
}

// Somebody who clicks "forgot password" four times must not leave four ways into
// their account lying in an inbox.
func TestAskingAgainClosesTheResetLinkAlreadySent(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "budi@example.test", "Budi")
	first := resetFor(t, ctx, q, user, 23, time.Now().Add(time.Hour))

	closed, err := q.ExpireOpenPasswordResetsForUser(ctx, user)
	if err != nil {
		t.Fatalf("ExpireOpenPasswordResetsForUser(): %v", err)
	}

	if closed != 1 {
		t.Fatalf("closed %d open resets, want 1", closed)
	}

	rows, err := q.UsePasswordReset(ctx, first.ID)
	if err != nil {
		t.Fatalf("UsePasswordReset(): %v", err)
	}

	if rows != 0 {
		t.Errorf("the superseded link still worked, changing %d rows", rows)
	}
}

// Closing one account's open links must not touch anybody else's. The statement
// is scoped by user_id, and this is what says so.
func TestClosingOneAccountsResetsSparesEverybodyElses(t *testing.T) {
	ctx, _, q := queriesTx(t)

	mine := createUser(t, ctx, q, "mine@example.test", "Mine")
	theirs := createUser(t, ctx, q, "theirs@example.test", "Theirs")

	resetFor(t, ctx, q, mine, 24, time.Now().Add(time.Hour))
	other := resetFor(t, ctx, q, theirs, 25, time.Now().Add(time.Hour))

	if _, err := q.ExpireOpenPasswordResetsForUser(ctx, mine); err != nil {
		t.Fatalf("ExpireOpenPasswordResetsForUser(): %v", err)
	}

	rows, err := q.UsePasswordReset(ctx, other.ID)
	if err != nil {
		t.Fatalf("UsePasswordReset(): %v", err)
	}

	if rows != 1 {
		t.Errorf("another account's link was closed too, so using it changed %d rows, want 1", rows)
	}
}

// A spent reset is the record that a password was replaced through it. Rewriting
// its deadline would rewrite that history.
func TestSupersedingLeavesSpentResetsAlone(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "budi@example.test", "Budi")
	spent := resetFor(t, ctx, q, user, 26, time.Now().Add(time.Hour))

	if _, err := q.UsePasswordReset(ctx, spent.ID); err != nil {
		t.Fatalf("UsePasswordReset(): %v", err)
	}

	closed, err := q.ExpireOpenPasswordResetsForUser(ctx, user)
	if err != nil {
		t.Fatalf("ExpireOpenPasswordResetsForUser(): %v", err)
	}

	if closed != 0 {
		t.Errorf("superseding touched %d spent resets, want 0", closed)
	}

	found, err := q.GetPasswordResetByTokenHash(ctx, digest(26))
	if err != nil {
		t.Fatalf("GetPasswordResetByTokenHash(): %v", err)
	}

	if !found.UsedAt.Valid {
		t.Error("the spent reset lost its used_at")
	}
}

// The reset counterpart of the rehash statement, and the difference is the
// point: there is no old hash to match on, because nobody knows it.
func TestSettingAPasswordNeedsNoOldHashAndHandsBackTheAccount(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "budi@example.test", "Budi")

	updated, err := q.SetUserPasswordHash(ctx, identityrepo.SetUserPasswordHashParams{
		ID:           user,
		PasswordHash: "argon2id$chosen-after-a-reset",
	})
	if err != nil {
		t.Fatalf("SetUserPasswordHash(): %v", err)
	}

	if updated.ID != user || updated.Email != "budi@example.test" || updated.Role != "contributor" {
		t.Errorf("SetUserPasswordHash returned %+v, want the account it just wrote", updated)
	}

	if updated.Timezone == "" {
		t.Error("the returned account has no timezone, so the caller would answer with a value that is not what is stored")
	}

	found, err := q.GetUserByID(ctx, user)
	if err != nil {
		t.Fatalf("GetUserByID(): %v", err)
	}

	if found.PasswordHash != "argon2id$chosen-after-a-reset" {
		t.Errorf("the stored hash is %q, want the one just set", found.PasswordHash)
	}
}

// A masked account must not be reachable through a link issued before it was
// masked. No rows rather than a silent zero, so the caller cannot mistake it for
// success.
func TestAMaskedAccountCannotHaveItsPasswordSet(t *testing.T) {
	ctx, _, q := queriesTx(t)

	user := createUser(t, ctx, q, "gone@example.test", "Gone")

	if _, err := q.SoftDeleteUser(ctx, user); err != nil {
		t.Fatalf("SoftDeleteUser(): %v", err)
	}

	if _, err := q.SetUserPasswordHash(ctx, identityrepo.SetUserPasswordHashParams{
		ID:           user,
		PasswordHash: "argon2id$should-never-land",
	}); !isNoRows(err) {
		t.Errorf("SetUserPasswordHash() on a masked account returned %v, want no rows", err)
	}
}

// What a reset is for. Leaving the old sessions alive would leave the account in
// the hands it is being taken out of.
func TestDeletingEverySessionSparesNothingOfTheAccountAndNothingOfAnother(t *testing.T) {
	ctx, _, q := queriesTx(t)

	mine := createUser(t, ctx, q, "mine@example.test", "Mine")
	theirs := createUser(t, ctx, q, "theirs@example.test", "Theirs")

	live := time.Now().Add(identitydom.SessionIdleWindow)

	issueSessionID(t, ctx, q, mine, live)
	issueSessionID(t, ctx, q, mine, live)
	issueSessionID(t, ctx, q, theirs, live)

	deleted, err := q.DeleteAllSessionsForUser(ctx, mine)
	if err != nil {
		t.Fatalf("DeleteAllSessionsForUser(): %v", err)
	}

	if deleted != 2 {
		t.Errorf("deleted %d sessions, want 2 — the caller's own session is not spared here", deleted)
	}

	left, err := q.ListSessionsByUser(ctx, mine)
	if err != nil {
		t.Fatalf("ListSessionsByUser(): %v", err)
	}

	if len(left) != 0 {
		t.Errorf("%d sessions survived the reset", len(left))
	}

	others, err := q.ListSessionsByUser(ctx, theirs)
	if err != nil {
		t.Fatalf("ListSessionsByUser(): %v", err)
	}

	if len(others) != 1 {
		t.Errorf("another account has %d sessions left, want 1", len(others))
	}
}

// The claim the whole confirmation step rests on, and the only way to test it is
// to commit: two transactions cannot race inside one. What this writes is
// removed again on the way out.
//
// Run sequentially, this passes even with the used_at guard removed -- which is
// exactly how a test of atomicity looks like it is testing something when it is
// not.
func TestConcurrentConfirmationsLetExactlyOneThrough(t *testing.T) {
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

	q := identityrepo.New(pool)

	user, err := q.CreateUser(ctx, identityrepo.CreateUserParams{
		Email:        "race-reset@example.test",
		Name:         "Race",
		PasswordHash: "argon2id$placeholder",
		Role:         "contributor",
	})
	if err != nil {
		t.Fatalf("CreateUser(): %v", err)
	}

	t.Cleanup(func() {
		_, _ = pool.Exec(context.Background(), `DELETE FROM password_resets WHERE user_id = $1`, user.ID)
		_, _ = pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, user.ID)
	})

	reset := resetFor(t, ctx, q, user.ID, 27, time.Now().Add(time.Hour))

	const confirmers = 20

	var (
		wg     sync.WaitGroup
		mu     sync.Mutex
		won    int
		start  = make(chan struct{})
		errsCh = make(chan error, confirmers)
	)

	for range confirmers {
		wg.Go(func() {
			<-start

			tx, err := pool.Begin(ctx)
			if err != nil {
				errsCh <- err

				return
			}

			defer func() { _ = tx.Rollback(ctx) }()

			rows, err := identityrepo.New(tx).UsePasswordReset(ctx, reset.ID)
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
		t.Errorf("a confirmation failed outright: %v", err)
	}

	if won != 1 {
		t.Errorf("%d of %d confirmations stamped the reset, want exactly 1", won, confirmers)
	}
}
