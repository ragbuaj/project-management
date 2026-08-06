package httpx

import (
	"net/http"
)

// Health answers /healthz: the process is alive and able to serve requests.
//
// It deliberately does not check the database, Redis, or any external service.
// Dependency checks belong to /readyz. Merging the two declares a healthy
// process dead whenever one of its dependencies is having trouble, and the
// orchestrator then restarts a process that did nothing wrong.
//
// This endpoint sits outside /api/v1 and is not part of the product contract,
// so it does not appear in docs/api/openapi.yaml.
func Health() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}
}
