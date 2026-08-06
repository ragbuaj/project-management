package httpx

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"time"
)

// ReadyCheck probes one dependency the application cannot serve without.
//
// Dependencies the application merely degrades without do not belong here.
// docs/nfr.md states the application keeps running when Redis is down:
// realtime stops, login rate limiting fails closed, and everything else is
// still served. Reporting that as not-ready would have the orchestrator pull a
// working instance out of rotation — the opposite of what the spec asks for.
type ReadyCheck struct {
	Name  string
	Probe func(ctx context.Context) error
}

type readyResponse struct {
	Status string            `json:"status"`
	Checks map[string]string `json:"checks"`
}

// Ready answers /readyz: every required dependency answers.
//
// The response body names each check and whether it passed, never why it
// failed. A driver error carries host names, user names, and sometimes the
// query — that detail goes to the log, and the caller gets a status.
func Ready(log *slog.Logger, timeout time.Duration, checks ...ReadyCheck) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), timeout)
		defer cancel()

		body := readyResponse{
			Status: "ok",
			Checks: make(map[string]string, len(checks)),
		}

		for _, c := range checks {
			if err := c.Probe(ctx); err != nil {
				body.Status = "unavailable"
				body.Checks[c.Name] = "unavailable"

				log.ErrorContext(ctx, "readiness check failed",
					slog.String("check", c.Name),
					slog.String("error", err.Error()),
				)

				continue
			}

			body.Checks[c.Name] = "ok"
		}

		status := http.StatusOK
		if body.Status != "ok" {
			status = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)

		if err := json.NewEncoder(w).Encode(body); err != nil {
			log.ErrorContext(ctx, "write readiness response", slog.String("error", err.Error()))
		}
	}
}
