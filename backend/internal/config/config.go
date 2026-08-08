// Package config loads configuration from the environment and validates it
// before the application serves any traffic. Bad configuration must stop the
// process at start-up, not surface as an error the first time some variable is
// read in the middle of a user request.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	netmail "net/mail"
	"net/netip"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/ragbuaj/project-management/backend/internal/httpx"
)

// ErrInvalid wraps every configuration failure so callers can tell it apart
// from other errors with errors.Is.
var ErrInvalid = errors.New("invalid configuration")

// Env distinguishes the deployment environment. It is the single switch that
// relaxes security settings locally — the Secure attribute on session cookies
// hangs off it too — so that no separate variable can be set wrong in
// production.
type Env string

const (
	EnvLocal      Env = "local"
	EnvProduction Env = "production"
)

// Bounds on how long each phase of a request may live. These are constants
// rather than environment variables: nobody has asked for them to be tunable,
// and every knob is a deferred decision that has to be maintained forever.
const (
	ReadTimeout     = 15 * time.Second
	WriteTimeout    = 30 * time.Second
	IdleTimeout     = 60 * time.Second
	ShutdownTimeout = 20 * time.Second

	// ReadyTimeout bounds the whole /readyz probe. It has to stay well under
	// whatever interval the orchestrator polls at, or a slow database turns
	// into piled-up probes rather than one honest failure.
	ReadyTimeout = 3 * time.Second
)

// RedisPoolSize is a constant rather than an environment variable because
// nothing has needed to tune it. Redis serves one command per connection and
// the commands here are single-key and short; DATABASE_MAX_CONNS is
// configurable because PostgreSQL runs a process per connection and the ceiling
// is a property of the server, which is not true here.
const RedisPoolSize = 10

const (
	defaultHTTPAddr        = ":8080"
	defaultLogLevel        = "info"
	defaultMaxRequestBytes = 1 << 20 // 1 MiB

	defaultDatabaseMaxConns = 20
	// PostgreSQL runs one process per connection, so an accidental extra zero
	// here takes the database down rather than making it faster.
	maxDatabaseMaxConns = 1000
)

// Config holds everything read at start-up.
//
// It grows alongside the phases that need it. Variables no code reads yet are
// deliberately absent: forcing someone to supply DATABASE_URL to run a server
// that never touches the database only produces a throwaway value, and
// throwaway values end up in production.
type Config struct {
	Env             Env
	BaseURL         *url.URL
	HTTPAddr        string
	LogLevel        slog.Level
	MaxRequestBytes int64

	DatabaseURL      string
	DatabaseMaxConns int32

	RedisURL string

	// TrustedProxies is the exact set of addresses whose X-Forwarded-For may
	// be believed (ADR-0010). Empty is the safe default and the correct value
	// for a server reached directly: with no entry here, no header is ever
	// read, and every request is attributed to the socket it arrived on.
	//
	// It is deliberately not "every private range". An attacker who reaches
	// the application from inside any such range would then be able to name
	// whatever client address they liked, and every rate limit in the
	// application would be one header away from useless.
	TrustedProxies httpx.TrustedProxies

	SMTP SMTP
}

// SMTP is what the mail sender is built from.
//
// Nothing here is validated beyond its shape. Whether the server answers is a
// run-time question, and a mail server that is down must not stop this
// application from starting: every part of it that is not mail still works.
type SMTP struct {
	Host string
	Port int
	// From is the address the installation sends as, parsed so that a
	// malformed value is a start-up failure rather than a message nobody can
	// send. A display name is accepted: "Project Management <no-reply@…>".
	From netmail.Address

	// Username and Password are empty when the server wants no credentials.
	// See requireSMTPCredentials for when that is allowed.
	Username string
	Password string
}

// defaultSMTPPort is submission over STARTTLS, which is what every provider
// wants and what mail.SMTP is built to speak. Mailpit listens on 1025 and has
// to say so.
const defaultSMTPPort = 587

// Lookup reads one environment variable. os.LookupEnv satisfies it. It is
// passed in so loading can be tested without touching the process environment.
type Lookup func(key string) (string, bool)

// Load reads and validates the whole configuration.
//
// Every problem is collected and reported at once. Reporting them one at a
// time forces the operator to restart the application to discover the next
// mistake.
func Load(lookup Lookup) (Config, error) {
	var (
		cfg      Config
		problems []error
	)

	rawEnv := value(lookup, "APP_ENV", "")
	switch Env(rawEnv) {
	case EnvLocal, EnvProduction:
		cfg.Env = Env(rawEnv)
	case "":
		problems = append(problems, missing("APP_ENV"))
	default:
		problems = append(problems, invalid("APP_ENV", rawEnv, "expected local or production"))
	}

	if rawURL := value(lookup, "APP_BASE_URL", ""); rawURL == "" {
		problems = append(problems, missing("APP_BASE_URL"))
	} else if u, err := parseBaseURL(rawURL, cfg.Env); err != nil {
		problems = append(problems, err)
	} else {
		cfg.BaseURL = u
	}

	rawLevel := strings.ToLower(value(lookup, "LOG_LEVEL", defaultLogLevel))
	switch rawLevel {
	case "debug":
		cfg.LogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = slog.LevelInfo
	case "warn":
		cfg.LogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = slog.LevelError
	default:
		problems = append(problems, invalid("LOG_LEVEL", rawLevel, "expected debug, info, warn or error"))
	}

	cfg.HTTPAddr = value(lookup, "HTTP_ADDR", defaultHTTPAddr)

	rawBytes := value(lookup, "MAX_REQUEST_BYTES", strconv.Itoa(defaultMaxRequestBytes))
	if n, err := strconv.ParseInt(rawBytes, 10, 64); err != nil || n <= 0 {
		problems = append(problems, invalid("MAX_REQUEST_BYTES", rawBytes, "expected a positive integer"))
	} else {
		cfg.MaxRequestBytes = n
	}

	if rawDB := value(lookup, "DATABASE_URL", ""); rawDB == "" {
		problems = append(problems, missing("DATABASE_URL"))
	} else if err := validateDatabaseURL(rawDB); err != nil {
		problems = append(problems, err)
	} else {
		cfg.DatabaseURL = rawDB
	}

	// Required even though the application survives Redis being down. Those are
	// different failures: a Redis that is unreachable is handled at run time,
	// a Redis nobody configured is a deployment that was never finished.
	if rawRedis := value(lookup, "REDIS_URL", ""); rawRedis == "" {
		problems = append(problems, missing("REDIS_URL"))
	} else if err := validateRedisURL(rawRedis); err != nil {
		problems = append(problems, err)
	} else {
		cfg.RedisURL = rawRedis
	}

	if smtp, errs := loadSMTP(lookup, cfg.Env); len(errs) > 0 {
		problems = append(problems, errs...)
	} else {
		cfg.SMTP = smtp
	}

	if trusted, err := parseTrustedProxies(value(lookup, "TRUSTED_PROXIES", "")); err != nil {
		problems = append(problems, err)
	} else {
		cfg.TrustedProxies = trusted
	}

	rawConns := value(lookup, "DATABASE_MAX_CONNS", strconv.Itoa(defaultDatabaseMaxConns))

	switch n, err := strconv.ParseInt(rawConns, 10, 32); {
	case err != nil, n <= 0, n > maxDatabaseMaxConns:
		problems = append(problems, invalid("DATABASE_MAX_CONNS", rawConns,
			fmt.Sprintf("expected an integer between 1 and %d", maxDatabaseMaxConns)))
	default:
		cfg.DatabaseMaxConns = int32(n)
	}

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(problems...))
	}

	return cfg, nil
}

// loadSMTP reads the mail settings, collecting every problem rather than
// stopping at the first for the same reason Load does.
//
// SMTP_USERNAME and SMTP_PASSWORD are required in production and optional
// locally. docs/environments.md wrote them as required everywhere, and that was
// wrong in the way that document itself warns about: Mailpit accepts neither,
// so demanding them locally produces a throwaway value, and throwaway values
// end up in production. This is the same shape APP_BASE_URL already has — a
// rule that only tightens where it means something (owner's decision,
// 2026-08-08).
func loadSMTP(lookup Lookup, env Env) (SMTP, []error) {
	var (
		smtp     SMTP
		problems []error
	)

	if smtp.Host = value(lookup, "SMTP_HOST", ""); smtp.Host == "" {
		problems = append(problems, missing("SMTP_HOST"))
	}

	rawPort := value(lookup, "SMTP_PORT", strconv.Itoa(defaultSMTPPort))
	if n, err := strconv.Atoi(rawPort); err != nil || n <= 0 || n > 65535 {
		problems = append(problems, invalid("SMTP_PORT", rawPort, "expected a port between 1 and 65535"))
	} else {
		smtp.Port = n
	}

	if rawFrom := value(lookup, "SMTP_FROM", ""); rawFrom == "" {
		problems = append(problems, missing("SMTP_FROM"))
	} else if from, err := netmail.ParseAddress(rawFrom); err != nil {
		problems = append(problems, invalid("SMTP_FROM", rawFrom, "expected an e-mail address"))
	} else {
		smtp.From = *from
	}

	smtp.Username = value(lookup, "SMTP_USERNAME", "")
	smtp.Password = value(lookup, "SMTP_PASSWORD", "")

	// One without the other is a mistake in every environment: mail.NewSMTP
	// refuses the pair anyway, and finding it at start-up beats finding it the
	// first time somebody is invited.
	if (smtp.Username == "") != (smtp.Password == "") {
		problems = append(problems, errors.New("SMTP_USERNAME and SMTP_PASSWORD come together: set both or neither"))
	} else if smtp.Username == "" && env == EnvProduction {
		problems = append(problems,
			missing("SMTP_USERNAME"),
			missing("SMTP_PASSWORD"))
	}

	return smtp, problems
}

// validateDatabaseURL checks the shape without ever echoing the value, and
// without wrapping the parse error either: a PostgreSQL URL carries the
// password, and url.Parse embeds the input in its own error message.
func validateDatabaseURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return invalidSecret("DATABASE_URL", "expected a postgres:// URL")
	}

	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return invalidSecret("DATABASE_URL", "expected the postgres:// or postgresql:// scheme")
	}

	if u.Host == "" {
		return invalidSecret("DATABASE_URL", "expected a host")
	}

	return nil
}

// validateRedisURL checks the shape without reporting what it was given: like
// DATABASE_URL, this string can carry a password.
func validateRedisURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return invalidSecret("REDIS_URL", "expected a redis:// URL")
	}

	// rediss:// is the TLS form and is what production should use.
	if u.Scheme != "redis" && u.Scheme != "rediss" {
		return invalidSecret("REDIS_URL", "expected the redis:// or rediss:// scheme")
	}

	if u.Host == "" {
		return invalidSecret("REDIS_URL", "expected a host")
	}

	return nil
}

// parseBaseURL accepts the public origin and rejects shapes that would produce
// broken links in e-mail, Telegram, and share links.
func parseBaseURL(raw string, env Env) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("APP_BASE_URL is invalid: %w", err)
	}

	if u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, invalid("APP_BASE_URL", raw, "expected an absolute http or https URL")
	}

	if u.Path != "" && u.Path != "/" {
		return nil, invalid("APP_BASE_URL", raw, "expected an origin without a path")
	}

	// Session cookies carry the Secure attribute in production. An http base
	// URL produces links over which such a cookie is never sent, and the
	// symptom shows up as "logging in never works" — a long way from the
	// cause.
	if env == EnvProduction && u.Scheme != "https" {
		return nil, invalid("APP_BASE_URL", raw, "expected https when APP_ENV is production")
	}

	u.Path = ""

	return u, nil
}

func value(lookup Lookup, key, fallback string) string {
	if v, ok := lookup(key); ok {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}

	return fallback
}

func missing(key string) error {
	return fmt.Errorf("%s is required", key)
}

// invalid echoes the rejected value so the cause is visible immediately. Do
// not use it for variables holding secrets — this message ends up in logs.
// Report only the variable name for those.
func invalid(key, got, want string) error {
	return fmt.Errorf("%s is invalid: got %q, %s", key, got, want)
}

// invalidSecret is the counterpart of invalid for variables that carry a
// secret. It names the variable and what was expected, and never the value.
func invalidSecret(key, want string) error {
	return fmt.Errorf("%s is invalid: %s", key, want)
}

// parseTrustedProxies reads a comma-separated list of CIDR ranges.
//
// A bare address is accepted and read as a single-host range, because that is
// how somebody will write it the first time and refusing it teaches nothing.
// Anything else is refused at start-up rather than silently dropped: a proxy
// that was meant to be trusted and is not would make the application attribute
// every request to that proxy, and the symptom — one rate-limit bucket for the
// whole world — looks nothing like a typo in an environment variable.
func parseTrustedProxies(raw string) (httpx.TrustedProxies, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}

	var out httpx.TrustedProxies

	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if prefix, err := netip.ParsePrefix(entry); err == nil {
			out = append(out, prefix)

			continue
		}

		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("TRUSTED_PROXIES: %q is neither a CIDR range nor an address", entry)
		}

		out = append(out, netip.PrefixFrom(addr, addr.BitLen()))
	}

	return out, nil
}
