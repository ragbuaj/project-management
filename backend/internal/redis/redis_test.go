package redis_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/redis"
)

func testRedisURL(t *testing.T) string {
	t.Helper()

	url := os.Getenv("TEST_REDIS_URL")
	if url == "" {
		t.Skip("TEST_REDIS_URL is not set; start compose or run this in CI")
	}

	return url
}

func TestNewRejectsAMalformedURL(t *testing.T) {
	t.Parallel()

	// A URL with no host is not here: go-redis quietly defaults it to
	// localhost:6379, which is exactly the failure worth refusing — but
	// refusing it belongs to config.validateRedisURL, so that it is rejected at
	// start-up with the variable named rather than here with only a URL.
	for _, raw := range []string{
		"://not a url",
		"http://localhost:6379",
		"redis://user@host:notaport",
	} {
		if _, err := redis.New(raw, 5); err == nil {
			t.Errorf("New(%q) = nil error, want a rejection", raw)
		}
	}
}

// A connection string carries a password, and the parse error from net/url
// embeds its input. Repeating it into a start-up log puts the password in
// every deployment's log for as long as the misconfiguration lasts.
func TestNewNeverEchoesTheConnectionString(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	_, err := redis.New("redis://user:"+password+"@ /bad", 5)
	if err == nil {
		t.Fatal("New() = nil error, want a rejection")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("the error leaks the connection string: %v", err)
	}
}

// The whole difference between this dependency and PostgreSQL. docs/nfr.md
// states the application keeps running when Redis is down, so New must hand
// back a usable client without having contacted anything — refusing here would
// turn a degraded feature set into an outage.
func TestNewDoesNotContactRedis(t *testing.T) {
	t.Parallel()

	// Port 1 is reserved and nothing listens there.
	client, err := redis.New("redis://127.0.0.1:1", 5)
	if err != nil {
		t.Fatalf("New() on an unreachable server: %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := redis.Ping(ctx, client); err == nil {
		t.Error("Ping() succeeded against a server nothing is listening on")
	}
}

// go-redis retries by default, which turns one dead-Redis command into three
// timeouts. A caller that has to decide what to degrade to should learn that
// Redis is down quickly rather than thoroughly.
func TestACommandAgainstADeadRedisFailsQuickly(t *testing.T) {
	t.Parallel()

	client, err := redis.New("redis://127.0.0.1:1", 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	// Generous enough not to be flaky on a loaded runner, tight enough to fail
	// if the retries come back: three dial timeouts would be six seconds.
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	started := time.Now()

	if err := redis.Ping(ctx, client); err == nil {
		t.Fatal("Ping() succeeded against a dead server")
	}

	if elapsed := time.Since(started); elapsed > 4*time.Second {
		t.Errorf("a failed command took %s; retries look like they are still on", elapsed)
	}
}

func TestPingSucceedsAgainstARealRedis(t *testing.T) {
	t.Parallel()

	client, err := redis.New(testRedisURL(t), 5)
	if err != nil {
		t.Fatalf("New(): %v", err)
	}

	t.Cleanup(func() { _ = client.Close() })

	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if err := redis.Ping(ctx, client); err != nil {
		t.Fatalf("Ping(): %v", err)
	}
}
