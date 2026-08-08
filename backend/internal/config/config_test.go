package config_test

import (
	"errors"
	"log/slog"
	"maps"
	"strings"
	"testing"

	"github.com/ragbuaj/project-management/backend/internal/config"
)

// lookupFrom builds a config.Lookup from a map so tests never touch the
// process environment and can run in parallel.
func lookupFrom(env map[string]string) config.Lookup {
	return func(key string) (string, bool) {
		v, ok := env[key]

		return v, ok
	}
}

// valid is the smallest configuration that must be accepted.
func valid() map[string]string {
	return map[string]string{
		"APP_ENV":      "local",
		"APP_BASE_URL": "http://localhost:8080",
		"DATABASE_URL": "postgres://pm:redacted-in-tests@localhost:5432/pm",
		"REDIS_URL":    "redis://localhost:6379",

		// No credentials: this is the local Mailpit, which accepts none. That
		// they may be absent here and may not in production is what
		// TestSMTPCredentialsAreRequiredOnlyInProduction is about.
		"SMTP_HOST": "localhost",
		"SMTP_FROM": "Project Management <no-reply@pm.example.test>",
	}
}

// with starts from valid() and overrides individual keys. An empty value drops
// the key, which is how an absent variable is expressed.
//
// Building cases this way means every failing case differs from a passing one
// in exactly the thing under test, and adding a new required variable does not
// mean editing every case.
func with(overrides map[string]string) map[string]string {
	env := valid()

	for k, v := range overrides {
		if v == "" {
			delete(env, k)

			continue
		}

		env[k] = v
	}

	return env
}

func TestLoadValid(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env    map[string]string
		assert func(t *testing.T, cfg config.Config)
	}{
		"defaults apply when optional variables are absent": {
			env: valid(),
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if cfg.HTTPAddr != ":8080" {
					t.Errorf("HTTPAddr = %q, want :8080", cfg.HTTPAddr)
				}

				if cfg.LogLevel != slog.LevelInfo {
					t.Errorf("LogLevel = %v, want info", cfg.LogLevel)
				}

				if cfg.MaxRequestBytes != 1<<20 {
					t.Errorf("MaxRequestBytes = %d, want %d", cfg.MaxRequestBytes, 1<<20)
				}

				if cfg.DatabaseMaxConns != 20 {
					t.Errorf("DatabaseMaxConns = %d, want 20", cfg.DatabaseMaxConns)
				}
			},
		},
		"production over https is accepted": {
			env: with(map[string]string{
				"APP_ENV":       "production",
				"APP_BASE_URL":  "https://pm.example.com",
				"SMTP_USERNAME": "pm",
				"SMTP_PASSWORD": "redacted-in-tests",
			}),
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if cfg.Env != config.EnvProduction {
					t.Errorf("Env = %q, want production", cfg.Env)
				}
			},
		},
		"a bare trailing slash is stripped, not rejected": {
			env: with(map[string]string{"APP_BASE_URL": "http://localhost:8080/"}),
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if got := cfg.BaseURL.String(); got != "http://localhost:8080" {
					t.Errorf("BaseURL = %q, want no trailing slash", got)
				}
			},
		},
		"the postgresql:// scheme is accepted too": {
			env: with(map[string]string{
				"DATABASE_URL": "postgresql://pm:redacted-in-tests@localhost:5432/pm",
			}),
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if cfg.DatabaseURL == "" {
					t.Error("DatabaseURL is empty")
				}
			},
		},
		"optional variables are honored when set": {
			env: with(map[string]string{
				"HTTP_ADDR":          "127.0.0.1:9999",
				"LOG_LEVEL":          "DEBUG",
				"MAX_REQUEST_BYTES":  "2048",
				"DATABASE_MAX_CONNS": "5",
			}),
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if cfg.HTTPAddr != "127.0.0.1:9999" {
					t.Errorf("HTTPAddr = %q", cfg.HTTPAddr)
				}

				if cfg.LogLevel != slog.LevelDebug {
					t.Errorf("LogLevel = %v, want debug", cfg.LogLevel)
				}

				if cfg.MaxRequestBytes != 2048 {
					t.Errorf("MaxRequestBytes = %d, want 2048", cfg.MaxRequestBytes)
				}

				if cfg.DatabaseMaxConns != 5 {
					t.Errorf("DatabaseMaxConns = %d, want 5", cfg.DatabaseMaxConns)
				}
			},
		},
		"a whitespace-only value counts as absent": {
			env: with(map[string]string{"HTTP_ADDR": "   "}),
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if cfg.HTTPAddr != ":8080" {
					t.Errorf("HTTPAddr = %q, want the default", cfg.HTTPAddr)
				}
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(lookupFrom(tc.env))
			if err != nil {
				t.Fatalf("Load() unexpected error: %v", err)
			}

			tc.assert(t, cfg)
		})
	}
}

func TestLoadInvalid(t *testing.T) {
	t.Parallel()

	tests := map[string]struct {
		env  map[string]string
		want []string // fragments the error message must contain
	}{
		"APP_ENV absent": {
			env:  with(map[string]string{"APP_ENV": ""}),
			want: []string{"APP_ENV is required"},
		},
		"APP_ENV unknown": {
			env:  with(map[string]string{"APP_ENV": "staging"}),
			want: []string{"APP_ENV is invalid", "staging"},
		},
		"APP_BASE_URL absent": {
			env:  with(map[string]string{"APP_BASE_URL": ""}),
			want: []string{"APP_BASE_URL is required"},
		},
		"APP_BASE_URL without a scheme": {
			env:  with(map[string]string{"APP_BASE_URL": "localhost:8080"}),
			want: []string{"APP_BASE_URL is invalid"},
		},
		"APP_BASE_URL carrying a path": {
			env:  with(map[string]string{"APP_BASE_URL": "http://localhost:8080/app"}),
			want: []string{"without a path"},
		},
		// The most valuable guard in this file: an http base URL in production
		// means the Secure session cookie is never sent, and the symptom shows
		// up as "logging in never works".
		"http is rejected in production": {
			env: with(map[string]string{
				"APP_ENV":      "production",
				"APP_BASE_URL": "http://pm.example.com",
			}),
			want: []string{"expected https when APP_ENV is production"},
		},
		"LOG_LEVEL unknown": {
			env:  with(map[string]string{"LOG_LEVEL": "trace"}),
			want: []string{"LOG_LEVEL is invalid", "trace"},
		},
		"MAX_REQUEST_BYTES not a number": {
			env:  with(map[string]string{"MAX_REQUEST_BYTES": "plenty"}),
			want: []string{"MAX_REQUEST_BYTES is invalid"},
		},
		"MAX_REQUEST_BYTES zero": {
			env:  with(map[string]string{"MAX_REQUEST_BYTES": "0"}),
			want: []string{"MAX_REQUEST_BYTES is invalid"},
		},
		"DATABASE_URL absent": {
			env:  with(map[string]string{"DATABASE_URL": ""}),
			want: []string{"DATABASE_URL is required"},
		},
		"DATABASE_URL with the wrong scheme": {
			env:  with(map[string]string{"DATABASE_URL": "mysql://pm@localhost:3306/pm"}),
			want: []string{"DATABASE_URL is invalid", "postgres://"},
		},
		"DATABASE_URL without a host": {
			env:  with(map[string]string{"DATABASE_URL": "postgres:///pm"}),
			want: []string{"DATABASE_URL is invalid", "host"},
		},
		"REDIS_URL absent": {
			env:  with(map[string]string{"REDIS_URL": ""}),
			want: []string{"REDIS_URL is required"},
		},
		"REDIS_URL with the wrong scheme": {
			env:  with(map[string]string{"REDIS_URL": "http://localhost:6379"}),
			want: []string{"REDIS_URL is invalid", "redis://"},
		},
		// go-redis silently defaults a hostless URL to localhost:6379, so a
		// production deployment would connect to nothing and look healthy.
		// Refusing it here names the variable; refusing it later would not.
		"REDIS_URL without a host": {
			env:  with(map[string]string{"REDIS_URL": "redis://"}),
			want: []string{"REDIS_URL is invalid", "host"},
		},
		"SMTP_HOST absent": {
			env:  with(map[string]string{"SMTP_HOST": ""}),
			want: []string{"SMTP_HOST is required"},
		},
		"SMTP_FROM absent": {
			env:  with(map[string]string{"SMTP_FROM": ""}),
			want: []string{"SMTP_FROM is required"},
		},
		// A From nobody can parse is a message nobody can send, and the first
		// time anyone finds out would be the first invitation.
		"SMTP_FROM that is not an address": {
			env:  with(map[string]string{"SMTP_FROM": "no-reply"}),
			want: []string{"SMTP_FROM is invalid"},
		},
		"SMTP_PORT that is not a port": {
			env:  with(map[string]string{"SMTP_PORT": "70000"}),
			want: []string{"SMTP_PORT is invalid", "between 1 and 65535"},
		},
		// mail.NewSMTP refuses the half-pair anyway. Refusing it at start-up
		// beats refusing it at the first invitation.
		"SMTP_USERNAME without SMTP_PASSWORD": {
			env:  with(map[string]string{"SMTP_USERNAME": "pm"}),
			want: []string{"SMTP_USERNAME and SMTP_PASSWORD come together"},
		},
		"SMTP_PASSWORD without SMTP_USERNAME": {
			env:  with(map[string]string{"SMTP_PASSWORD": "redacted-in-tests"}),
			want: []string{"SMTP_USERNAME and SMTP_PASSWORD come together"},
		},
		"DATABASE_MAX_CONNS zero": {
			env:  with(map[string]string{"DATABASE_MAX_CONNS": "0"}),
			want: []string{"DATABASE_MAX_CONNS is invalid"},
		},
		// An accidental extra zero takes the database down rather than making
		// it faster: PostgreSQL runs one process per connection.
		"DATABASE_MAX_CONNS beyond the ceiling": {
			env:  with(map[string]string{"DATABASE_MAX_CONNS": "100000"}),
			want: []string{"DATABASE_MAX_CONNS is invalid", "between 1 and 1000"},
		},
		// Reporting one problem at a time forces the operator to restart the
		// application to discover the next mistake.
		"every problem is reported at once": {
			env: with(map[string]string{
				"APP_ENV":      "",
				"APP_BASE_URL": "",
				"LOG_LEVEL":    "trace",
			}),
			want: []string{"APP_ENV is required", "APP_BASE_URL is required", "LOG_LEVEL is invalid"},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			cfg, err := config.Load(lookupFrom(tc.env))
			if err == nil {
				t.Fatalf("Load() = %+v, want an error", cfg)
			}

			if !errors.Is(err, config.ErrInvalid) {
				t.Errorf("errors.Is(err, ErrInvalid) = false, error was: %v", err)
			}

			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error message does not contain %q\ngot: %v", want, err)
				}
			}
		})
	}
}

// A connection string carries the password, and a start-up error is the first
// thing anyone pastes into a chat window when an application refuses to boot.
func TestLoadNeverEchoesTheDatabasePassword(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	broken := maps.Clone(valid())
	broken["DATABASE_URL"] = "mysql://pm:" + password + "@localhost:3306/pm"

	_, err := config.Load(lookupFrom(broken))
	if err == nil {
		t.Fatal("Load() = nil, want an error")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("the error message leaks the password: %v", err)
	}

	if !strings.Contains(err.Error(), "DATABASE_URL is invalid") {
		t.Errorf("the error message does not name the variable: %v", err)
	}
}

// REDIS_URL carries a password too, and a Redis reachable from the internet
// with a weak one is a well-known way in.
func TestLoadNeverEchoesTheRedisPassword(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	broken := maps.Clone(valid())
	broken["REDIS_URL"] = "amqp://pm:" + password + "@localhost:6379"

	_, err := config.Load(lookupFrom(broken))
	if err == nil {
		t.Fatal("Load() = nil, want an error")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("the error message leaks the password: %v", err)
	}

	if !strings.Contains(err.Error(), "REDIS_URL is invalid") {
		t.Errorf("the error message does not name the variable: %v", err)
	}
}

// Empty is the safe default and the correct value for a server reached
// directly: with nothing here, no X-Forwarded-For is ever believed.
func TestTrustedProxiesDefaultsToNothing(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookupFrom(valid()))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if len(cfg.TrustedProxies) != 0 {
		t.Errorf("TrustedProxies = %v, want empty", cfg.TrustedProxies)
	}
}

func TestTrustedProxiesAcceptsRangesAndBareAddresses(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookupFrom(with(map[string]string{
		"TRUSTED_PROXIES": "10.0.0.0/8, 203.0.113.7 ,2001:db8::/32",
	})))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if len(cfg.TrustedProxies) != 3 {
		t.Fatalf("got %d prefixes, want 3: %v", len(cfg.TrustedProxies), cfg.TrustedProxies)
	}

	// A bare address is a single host, not a range that quietly includes its
	// neighbors.
	if got := cfg.TrustedProxies[1].String(); got != "203.0.113.7/32" {
		t.Errorf("bare address became %q, want a single-host range", got)
	}
}

// A proxy that was meant to be trusted and is not makes the application
// attribute every request to that proxy — one rate-limit bucket for the whole
// world, a symptom that looks nothing like a typo in an environment variable.
// So a malformed entry stops start-up instead.
func TestAMalformedTrustedProxyStopsStartUp(t *testing.T) {
	t.Parallel()

	for _, raw := range []string{"10.0.0.0/99", "not-an-address", "10.0.0.1-10.0.0.9"} {
		_, err := config.Load(lookupFrom(with(map[string]string{"TRUSTED_PROXIES": raw})))
		if !errors.Is(err, config.ErrInvalid) {
			t.Errorf("TRUSTED_PROXIES=%q loaded with err = %v", raw, err)
		}
	}
}

// The rule the owner decided on 2026-08-08. docs/environments.md had written
// both as required everywhere, which is wrong in the way that same document
// warns about: Mailpit accepts neither, so demanding them locally produces a
// throwaway value, and throwaway values end up in production.
//
// It is the shape APP_BASE_URL already has — a rule that only tightens where
// it means something.
func TestSMTPCredentialsAreRequiredOnlyInProduction(t *testing.T) {
	t.Parallel()

	t.Run("absent is fine locally", func(t *testing.T) {
		t.Parallel()

		cfg, err := config.Load(lookupFrom(valid()))
		if err != nil {
			t.Fatalf("Load(): %v", err)
		}

		if cfg.SMTP.Username != "" || cfg.SMTP.Password != "" {
			t.Errorf("credentials were invented: %q", cfg.SMTP.Username)
		}
	})

	t.Run("absent is refused in production", func(t *testing.T) {
		t.Parallel()

		env := with(map[string]string{
			"APP_ENV":      "production",
			"APP_BASE_URL": "https://pm.example.com",
		})

		_, err := config.Load(lookupFrom(env))
		if err == nil {
			t.Fatal("Load() = nil, want an error")
		}

		for _, want := range []string{"SMTP_USERNAME is required", "SMTP_PASSWORD is required"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %v does not mention %q", err, want)
			}
		}
	})
}

// SMTP_PASSWORD is a secret, and a start-up error is the first thing anyone
// pastes into a chat window when an application refuses to boot.
func TestLoadNeverEchoesTheSMTPPassword(t *testing.T) {
	t.Parallel()

	const password = "correct-horse-battery-staple"

	// A configuration that fails for an unrelated reason, so the whole error
	// is built and every variable gets its chance to appear in it.
	broken := with(map[string]string{
		"SMTP_PASSWORD": password,
		"SMTP_PORT":     "70000",
	})

	_, err := config.Load(lookupFrom(broken))
	if err == nil {
		t.Fatal("Load() = nil, want an error")
	}

	if strings.Contains(err.Error(), password) {
		t.Fatalf("the error message leaks the password: %v", err)
	}
}

// The port most providers want for submission over STARTTLS. Mailpit listens
// on 1025 and has to say so, which is what makes this a default rather than a
// constant.
func TestSMTPPortDefaultsToSubmission(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookupFrom(valid()))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.SMTP.Port != 587 {
		t.Errorf("SMTP.Port = %d, want 587", cfg.SMTP.Port)
	}
}

// A display name in SMTP_FROM is what a person sees in their inbox, and
// dropping it would make every message come from a bare address.
func TestSMTPFromKeepsItsDisplayName(t *testing.T) {
	t.Parallel()

	cfg, err := config.Load(lookupFrom(valid()))
	if err != nil {
		t.Fatalf("Load(): %v", err)
	}

	if cfg.SMTP.From.Name != "Project Management" || cfg.SMTP.From.Address != "no-reply@pm.example.test" {
		t.Errorf("SMTP.From = %+v, want the name and the address kept apart", cfg.SMTP.From)
	}
}
