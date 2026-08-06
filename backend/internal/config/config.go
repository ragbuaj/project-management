// Package config loads configuration from the environment and validates it
// before the application serves any traffic. Bad configuration must stop the
// process at start-up, not surface as an error the first time some variable is
// read in the middle of a user request.
package config

import (
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"strconv"
	"strings"
	"time"
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
)

const (
	defaultHTTPAddr        = ":8080"
	defaultLogLevel        = "info"
	defaultMaxRequestBytes = 1 << 20 // 1 MiB
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
}

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

	if len(problems) > 0 {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(problems...))
	}

	return cfg, nil
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
