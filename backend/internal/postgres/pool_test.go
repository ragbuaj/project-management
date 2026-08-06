package postgres_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/postgres"
)

// testDatabaseURL returns the connection string for the integration tests, or
// skips them.
//
// Mocking the SQL driver is not an option here: what these tests verify is
// that PostgreSQL itself received the settings, and a mock would only be
// testing the mock. Locally this comes from `docker compose up -d postgres`;
// in CI it comes from the service container.
func testDatabaseURL(t *testing.T) string {
	t.Helper()

	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set; start compose or run this in CI")
	}

	return url
}

func TestNewRejectsAMalformedURL(t *testing.T) {
	t.Parallel()

	_, err := postgres.New(t.Context(), "://not a url", 5)
	if !errors.Is(err, postgres.ErrInvalidURL) {
		t.Fatalf("err = %v, want ErrInvalidURL", err)
	}
}

// The connection string carries the password, so a failure here must name the
// variable and nothing else.
func TestNewNeverEchoesTheConnectionString(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	_, err := postgres.New(t.Context(), "://"+password, 5)
	if err == nil {
		t.Fatal("New() = nil, want an error")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("the error leaks the connection string: %v", err)
	}
}

func TestNewConnects(t *testing.T) {
	t.Parallel()

	pool, err := postgres.New(t.Context(), testDatabaseURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	var one int
	if err := pool.QueryRow(ctx, "SELECT 1").Scan(&one); err != nil {
		t.Fatalf("SELECT 1: %v", err)
	}

	if one != 1 {
		t.Errorf("SELECT 1 = %d", one)
	}
}

// A timeout that is configured but never reaches the server is worse than no
// timeout: it reads as protection that is not there. These read the settings
// back from PostgreSQL rather than from the pgx config struct.
func TestNewAppliesServerSideTimeouts(t *testing.T) {
	t.Parallel()

	pool, err := postgres.New(t.Context(), testDatabaseURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(pool.Close)

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	tests := map[string]string{
		"statement_timeout":                   "15s",
		"idle_in_transaction_session_timeout": "30s",
	}

	for setting, want := range tests {
		var got string
		if err := pool.QueryRow(ctx, "SHOW "+setting).Scan(&got); err != nil {
			t.Fatalf("SHOW %s: %v", setting, err)
		}

		if got != want {
			t.Errorf("%s = %q, want %q", setting, got, want)
		}
	}
}

func TestNewFailsFastWhenTheDatabaseIsUnreachable(t *testing.T) {
	t.Parallel()

	// Port 1 is reserved and nothing listens there, so this fails at connect
	// rather than at authentication.
	_, err := postgres.New(t.Context(), "postgres://pm@127.0.0.1:1/pm", 5)
	if err == nil {
		t.Fatal("New() = nil, want an error")
	}
}
