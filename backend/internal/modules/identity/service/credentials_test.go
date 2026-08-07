package service_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"golang.org/x/crypto/argon2"

	identitydom "github.com/ragbuaj/project-management/backend/internal/modules/identity/domain"
	identityrepo "github.com/ragbuaj/project-management/backend/internal/modules/identity/repository"
	identitysvc "github.com/ragbuaj/project-management/backend/internal/modules/identity/service"
)

const testPassword = "kuda benar baterai steples"

// userStub stands in for the user half of the repository.
type userStub struct {
	rows map[string]identityrepo.GetUserByEmailRow

	updates []identityrepo.UpdateUserPasswordHashParams

	failWith       error
	failUpdateWith error
}

func (s *userStub) GetUserByEmail(_ context.Context, email string) (identityrepo.GetUserByEmailRow, error) {
	if s.failWith != nil {
		return identityrepo.GetUserByEmailRow{}, s.failWith
	}

	row, ok := s.rows[strings.ToLower(email)]
	if !ok {
		return identityrepo.GetUserByEmailRow{}, pgx.ErrNoRows
	}

	return row, nil
}

func (s *userStub) UpdateUserPasswordHash(_ context.Context, arg identityrepo.UpdateUserPasswordHashParams) (int64, error) {
	if s.failUpdateWith != nil {
		return 0, s.failUpdateWith
	}

	s.updates = append(s.updates, arg)

	return 1, nil
}

// seedUser puts an account behind email with the given stored hash.
func seedUser(t *testing.T, email, hash string) *userStub {
	t.Helper()

	return &userStub{rows: map[string]identityrepo.GetUserByEmailRow{
		strings.ToLower(email): {
			ID:           "user-1",
			Email:        email,
			Name:         "Member",
			PasswordHash: hash,
			Timezone:     "Asia/Jakarta",
			Role:         "contributor",
		},
	}}
}

func newCredentials(t *testing.T, store identitysvc.UserStore, log *slog.Logger) *identitysvc.Credentials {
	t.Helper()

	credentials, err := identitysvc.NewCredentials(store, log)
	if err != nil {
		t.Fatalf("NewCredentials(): %v", err)
	}

	return credentials
}

func hashFor(t *testing.T, password string) string {
	t.Helper()

	hash, err := identitydom.HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword(): %v", err)
	}

	return hash
}

func TestTheRightPasswordReturnsTheAccountBehindIt(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "member@example.test", hashFor(t, testPassword))

	who, err := newCredentials(t, store, slog.New(slog.DiscardHandler)).
		Authenticate(t.Context(), "member@example.test", testPassword)
	if err != nil {
		t.Fatalf("Authenticate(): %v", err)
	}

	want := identitysvc.User{
		ID:       "user-1",
		Email:    "member@example.test",
		Name:     "Member",
		Timezone: "Asia/Jakarta",
		Role:     "contributor",
	}

	if who != want {
		t.Errorf("Authenticate() = %+v, want %+v", who, want)
	}
}

// Addresses are not case sensitive, and the index that finds them is on
// lower(email). Someone who registered with a capital must still get in.
func TestTheAddressIsNotCaseSensitive(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "Member@Example.test", hashFor(t, testPassword))

	if _, err := newCredentials(t, store, slog.New(slog.DiscardHandler)).
		Authenticate(t.Context(), "member@EXAMPLE.test", testPassword); err != nil {
		t.Errorf("Authenticate() with a differently cased address: %v", err)
	}
}

// An address with no account and a wrong password must be one answer. Two
// answers make the login endpoint a way to ask which addresses are registered.
func TestAnUnknownAddressAndAWrongPasswordAreOneAnswer(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "member@example.test", hashFor(t, testPassword))
	credentials := newCredentials(t, store, slog.New(slog.DiscardHandler))

	cases := []struct {
		name     string
		email    string
		password string
	}{
		{"an address with no account", "nobody@example.test", testPassword},
		{"the right address and the wrong password", "member@example.test", "kuda salah baterai steples"},
		{"an empty password", "member@example.test", ""},
		{"a password too short to have been registered", "member@example.test", "short"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			who, err := credentials.Authenticate(t.Context(), c.email, c.password)
			if !errors.Is(err, identitysvc.ErrInvalidCredentials) {
				t.Fatalf("Authenticate() = %+v, %v; want ErrInvalidCredentials", who, err)
			}

			if who != (identitysvc.User{}) {
				t.Errorf("Authenticate() returned %+v alongside the refusal", who)
			}
		})
	}
}

// The other half of not leaking which addresses exist. Answering "no account"
// without hashing anything returns in microseconds while a wrong password
// costs an Argon2id run, and a stopwatch tells them apart.
func TestAnUnknownAddressCostsTheSameWorkAsAWrongPassword(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "member@example.test", hashFor(t, testPassword))
	credentials := newCredentials(t, store, slog.New(slog.DiscardHandler))

	elapsed := func(email string) time.Duration {
		started := time.Now()

		if _, err := credentials.Authenticate(t.Context(), email, "kuda salah baterai steples"); err == nil {
			t.Fatal("Authenticate() succeeded where it should have refused")
		}

		return time.Since(started)
	}

	unknown := elapsed("nobody@example.test")
	known := elapsed("member@example.test")

	// Both should be an Argon2id run — tens of milliseconds. The bar is
	// deliberately loose: this test has to survive a shared CI runner, and a
	// missing decoy is a difference of three orders of magnitude, not of
	// scheduling noise.
	if unknown < known/4 {
		t.Errorf("an unknown address answered in %v against %v for a wrong password, "+
			"so the endpoint can be used to check which addresses have accounts", unknown, known)
	}
}

// The only moment a stored hash can be upgraded is a successful login, because
// it is the only moment the password is in memory.
func TestASuccessfulLoginRewritesAHashMadeAtALowerCost(t *testing.T) {
	t.Parallel()

	older := hashAtLowerCost(t, testPassword)

	store := seedUser(t, "member@example.test", older)

	if _, err := newCredentials(t, store, slog.New(slog.DiscardHandler)).
		Authenticate(t.Context(), "member@example.test", testPassword); err != nil {
		t.Fatalf("Authenticate() against the older hash: %v", err)
	}

	if len(store.updates) != 1 {
		t.Fatalf("the older hash was rewritten %d times, want 1", len(store.updates))
	}

	update := store.updates[0]

	if update.CurrentHash != older {
		t.Error("the rewrite does not name the hash it replaces, so two racing logins could overwrite each other")
	}

	if identitydom.NeedsRehash(update.NewHash) {
		t.Error("the rewritten hash still asks to be rewritten")
	}

	if err := identitydom.VerifyPassword(update.NewHash, testPassword); err != nil {
		t.Errorf("the rewritten hash does not verify the password it was made from: %v", err)
	}
}

// The other half: a hash already at the current cost is not rewritten, or
// every login would carry a write it does not need.
func TestALoginAgainstACurrentHashWritesNothing(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "member@example.test", hashFor(t, testPassword))

	if _, err := newCredentials(t, store, slog.New(slog.DiscardHandler)).
		Authenticate(t.Context(), "member@example.test", testPassword); err != nil {
		t.Fatalf("Authenticate(): %v", err)
	}

	if len(store.updates) != 0 {
		t.Errorf("a hash at the current cost was rewritten anyway: %+v", store.updates)
	}
}

// A store that cannot take the rewrite must not cost anyone their login. The
// account stays reachable at the cost it already had.
func TestAFailedRehashStillSignsTheUserIn(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "member@example.test", hashAtLowerCost(t, testPassword))
	store.failUpdateWith = errors.New("connection reset")

	var buf bytes.Buffer

	who, err := newCredentials(t, store, capturingLogger(&buf)).
		Authenticate(t.Context(), "member@example.test", testPassword)
	if err != nil {
		t.Fatalf("Authenticate() = %v, want the login to succeed", err)
	}

	if who.ID != "user-1" {
		t.Errorf("Authenticate() returned %+v, want the seeded account", who)
	}

	if !strings.Contains(buf.String(), "not rehashed") {
		t.Errorf("a failed rehash was swallowed without a word: %q", buf.String())
	}
}

// A row this application could not have written is not a wrong password. Both
// refusals look the same to the client, but only one is worth waking somebody
// up for.
func TestAnUnreadableStoredHashIsLoggedRatherThanPassedOffAsAWrongPassword(t *testing.T) {
	t.Parallel()

	store := seedUser(t, "member@example.test", "$2y$12$notanargon2idhashatall")

	var buf bytes.Buffer

	_, err := newCredentials(t, store, capturingLogger(&buf)).
		Authenticate(t.Context(), "member@example.test", testPassword)
	if !errors.Is(err, identitysvc.ErrInvalidCredentials) {
		t.Fatalf("Authenticate() = %v, want ErrInvalidCredentials", err)
	}

	if !strings.Contains(buf.String(), "unreadable") {
		t.Errorf("an unreadable stored hash was not logged: %q", buf.String())
	}
}

// A store failure is not a wrong password. Answering 401 to a broken database
// tells people their password stopped working.
func TestAStoreFailureIsNotReportedAsAWrongPassword(t *testing.T) {
	t.Parallel()

	store := &userStub{failWith: errors.New("connection reset")}

	_, err := newCredentials(t, store, slog.New(slog.DiscardHandler)).
		Authenticate(t.Context(), "member@example.test", testPassword)
	if err == nil {
		t.Fatal("Authenticate() succeeded against a failing store")
	}

	if errors.Is(err, identitysvc.ErrInvalidCredentials) {
		t.Errorf("a store failure was reported as ErrInvalidCredentials: %v", err)
	}
}

// hashAtLowerCost produces a real hash at parameters this application no longer
// writes. It is the only way to exercise the upgrade path without keeping the
// old parameters alive in the production code.
func hashAtLowerCost(t *testing.T, password string) string {
	t.Helper()

	const (
		memoryKiB = 8192
		passes    = 1
		threads   = 1
	)

	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, passes, memoryKiB, threads, 32)

	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memoryKiB, passes, threads,
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(key))
}
