package config_test

import (
	"errors"
	"log/slog"
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

// valid is the smallest configuration that must be accepted. Every failing
// case below is built by breaking exactly one variable from it, so that what
// is under test really is that variable.
func valid() map[string]string {
	return map[string]string{
		"APP_ENV":      "local",
		"APP_BASE_URL": "http://localhost:8080",
	}
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
			},
		},
		"production over https is accepted": {
			env: map[string]string{
				"APP_ENV":      "production",
				"APP_BASE_URL": "https://pm.example.com",
			},
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if cfg.Env != config.EnvProduction {
					t.Errorf("Env = %q, want production", cfg.Env)
				}
			},
		},
		"a bare trailing slash is stripped, not rejected": {
			env: map[string]string{
				"APP_ENV":      "local",
				"APP_BASE_URL": "http://localhost:8080/",
			},
			assert: func(t *testing.T, cfg config.Config) {
				t.Helper()

				if got := cfg.BaseURL.String(); got != "http://localhost:8080" {
					t.Errorf("BaseURL = %q, want no trailing slash", got)
				}
			},
		},
		"optional variables are honored when set": {
			env: map[string]string{
				"APP_ENV":           "local",
				"APP_BASE_URL":      "http://localhost:8080",
				"HTTP_ADDR":         "127.0.0.1:9999",
				"LOG_LEVEL":         "DEBUG",
				"MAX_REQUEST_BYTES": "2048",
			},
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
			},
		},
		"a whitespace-only value counts as absent": {
			env: map[string]string{
				"APP_ENV":      "local",
				"APP_BASE_URL": "http://localhost:8080",
				"HTTP_ADDR":    "   ",
			},
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
			env:  map[string]string{"APP_BASE_URL": "http://localhost:8080"},
			want: []string{"APP_ENV is required"},
		},
		"APP_ENV unknown": {
			env: map[string]string{
				"APP_ENV":      "staging",
				"APP_BASE_URL": "http://localhost:8080",
			},
			want: []string{"APP_ENV is invalid", "staging"},
		},
		"APP_BASE_URL absent": {
			env:  map[string]string{"APP_ENV": "local"},
			want: []string{"APP_BASE_URL is required"},
		},
		"APP_BASE_URL without a scheme": {
			env: map[string]string{
				"APP_ENV":      "local",
				"APP_BASE_URL": "localhost:8080",
			},
			want: []string{"APP_BASE_URL is invalid"},
		},
		"APP_BASE_URL carrying a path": {
			env: map[string]string{
				"APP_ENV":      "local",
				"APP_BASE_URL": "http://localhost:8080/app",
			},
			want: []string{"without a path"},
		},
		// The most valuable guard in this file: an http base URL in production
		// means the Secure session cookie is never sent, and the symptom shows
		// up as "logging in never works".
		"http is rejected in production": {
			env: map[string]string{
				"APP_ENV":      "production",
				"APP_BASE_URL": "http://pm.example.com",
			},
			want: []string{"expected https when APP_ENV is production"},
		},
		"LOG_LEVEL unknown": {
			env: map[string]string{
				"APP_ENV":      "local",
				"APP_BASE_URL": "http://localhost:8080",
				"LOG_LEVEL":    "trace",
			},
			want: []string{"LOG_LEVEL is invalid", "trace"},
		},
		"MAX_REQUEST_BYTES not a number": {
			env: map[string]string{
				"APP_ENV":           "local",
				"APP_BASE_URL":      "http://localhost:8080",
				"MAX_REQUEST_BYTES": "plenty",
			},
			want: []string{"MAX_REQUEST_BYTES is invalid"},
		},
		"MAX_REQUEST_BYTES zero": {
			env: map[string]string{
				"APP_ENV":           "local",
				"APP_BASE_URL":      "http://localhost:8080",
				"MAX_REQUEST_BYTES": "0",
			},
			want: []string{"MAX_REQUEST_BYTES is invalid"},
		},
		// Reporting one problem at a time forces the operator to restart the
		// application to discover the next mistake.
		"every problem is reported at once": {
			env:  map[string]string{"LOG_LEVEL": "trace"},
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
