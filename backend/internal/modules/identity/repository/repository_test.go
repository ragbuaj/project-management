package repository_test

import (
	"context"
	"errors"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

// queriesTx hands back a Queries bound to a transaction that the caller never
// commits, so the shared test database is left exactly as it was found.
//
// The migrate-and-connect dance is repeated here rather than shared with
// internal/postgres: a helper package existing only to be imported by tests
// would be the `helpers` package rules/20-go.md forbids, and twenty lines of
// setup is cheaper than the precedent.
func queriesTx(t *testing.T) (context.Context, pgx.Tx, *identityrepo.Queries) {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; start compose or run this in CI")
	}

	if err := postgres.Migrate(context.Background(), url); err != nil {
		t.Fatalf("Migrate(): %v", err)
	}

	pool, err := postgres.New(t.Context(), url, 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	t.Cleanup(cancel)

	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin: %v", err)
	}

	t.Cleanup(func() { _ = tx.Rollback(ctx) })

	return ctx, tx, identityrepo.New(tx)
}

func createUser(t *testing.T, ctx context.Context, q *identityrepo.Queries, email, name string) string {
	t.Helper()

	user, err := q.CreateUser(ctx, identityrepo.CreateUserParams{
		Email:        email,
		Name:         name,
		PasswordHash: "argon2id$placeholder",
		IsOwner:      false,
	})
	if err != nil {
		t.Fatalf("CreateUser(%s): %v", email, err)
	}

	return user.ID
}

// The reason every read names users_live rather than users. Soft delete is
// enforced by the view, not by each query remembering a WHERE clause — and
// this proves the generated code inherits that enforcement rather than merely
// being written next to it.
func TestASoftDeletedUserIsInvisibleToEveryRead(t *testing.T) {
	ctx, tx, q := queriesTx(t)

	// The shared database carries rows from other tests; clearing them makes
	// the ListUsers count below mean something. Rolled back with everything
	// else.
	if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}

	id := createUser(t, ctx, q, "present@example.test", "Present")
	createUser(t, ctx, q, "absent@example.test", "Absent")

	before, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers(): %v", err)
	}

	if len(before) != 2 {
		t.Fatalf("ListUsers() returned %d users before the delete, want 2", len(before))
	}

	rows, err := q.SoftDeleteUser(ctx, id)
	if err != nil {
		t.Fatalf("SoftDeleteUser(): %v", err)
	}

	if rows != 1 {
		t.Fatalf("SoftDeleteUser() masked %d rows, want 1", rows)
	}

	if _, err := q.GetUserByID(ctx, id); !isNoRows(err) {
		t.Errorf("GetUserByID() on a masked account returned %v, want no rows", err)
	}

	if _, err := q.GetUserByEmail(ctx, "present@example.test"); !isNoRows(err) {
		t.Errorf("GetUserByEmail() on a masked account returned %v, want no rows", err)
	}

	after, err := q.ListUsers(ctx)
	if err != nil {
		t.Fatalf("ListUsers(): %v", err)
	}

	if len(after) != 1 {
		t.Errorf("ListUsers() returned %d users after the delete, want 1", len(after))
	}
}

// Masking replaces the address rather than blanking it, which is what frees
// the original for reuse through the partial unique index. Someone who leaves
// and comes back must be able to sign up with the address they had.
func TestMaskingFreesTheAddressForReuse(t *testing.T) {
	ctx, tx, q := queriesTx(t)

	if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}

	id := createUser(t, ctx, q, "returning@example.test", "Returning")

	if _, err := q.SoftDeleteUser(ctx, id); err != nil {
		t.Fatalf("SoftDeleteUser(): %v", err)
	}

	again := createUser(t, ctx, q, "returning@example.test", "Returning again")

	if again == id {
		t.Fatal("the second account reused the first one's id")
	}

	found, err := q.GetUserByEmail(ctx, "returning@example.test")
	if err != nil {
		t.Fatalf("GetUserByEmail(): %v", err)
	}

	if found.ID != again {
		t.Errorf("GetUserByEmail() returned the masked account, not the new one")
	}
}

// Addresses are not case sensitive, and users_email_key indexes lower(email)
// for this lookup. A login that fails because of a capital letter is a support
// ticket, not a security control.
func TestLoginLookupIgnoresCase(t *testing.T) {
	ctx, tx, q := queriesTx(t)

	if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}

	id := createUser(t, ctx, q, "Mixed.Case@Example.test", "Mixed")

	for _, spelling := range []string{
		"Mixed.Case@Example.test",
		"mixed.case@example.test",
		"MIXED.CASE@EXAMPLE.TEST",
	} {
		found, err := q.GetUserByEmail(ctx, spelling)
		if err != nil {
			t.Errorf("GetUserByEmail(%q): %v", spelling, err)

			continue
		}

		if found.ID != id {
			t.Errorf("GetUserByEmail(%q) returned another account", spelling)
		}
	}
}

// SoftDeleteUser reports how many rows it changed, so a caller can tell "there
// was no such account" from "it was already masked". Both must be zero, and a
// service that cannot tell them apart from success will report the wrong thing
// to whoever asked.
func TestMaskingAnAlreadyMaskedAccountChangesNothing(t *testing.T) {
	ctx, tx, q := queriesTx(t)

	if _, err := tx.Exec(ctx, `DELETE FROM users`); err != nil {
		t.Fatalf("clear users: %v", err)
	}

	id := createUser(t, ctx, q, "once@example.test", "Once")

	if _, err := q.SoftDeleteUser(ctx, id); err != nil {
		t.Fatalf("first SoftDeleteUser(): %v", err)
	}

	rows, err := q.SoftDeleteUser(ctx, id)
	if err != nil {
		t.Fatalf("second SoftDeleteUser(): %v", err)
	}

	if rows != 0 {
		t.Errorf("masking an already masked account changed %d rows, want 0", rows)
	}

	rows, err = q.SoftDeleteUser(ctx, "00000000-0000-0000-0000-000000000000")
	if err != nil {
		t.Fatalf("SoftDeleteUser() on a missing account: %v", err)
	}

	if rows != 0 {
		t.Errorf("masking a missing account changed %d rows, want 0", rows)
	}
}

func isNoRows(err error) bool {
	return errors.Is(err, pgx.ErrNoRows)
}
