package redirect

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const healthCheckTimeout time.Duration = 2 * time.Second

// HealthHandler answers GET /healthz with "ok" after a trivial database query,
// or 503 if the database does not answer. The failure detail is logged, not
// sent to the client.
func HealthHandler(pinger store.Pinger, logger *slog.Logger) http.HandlerFunc {
	return func(responseWriter http.ResponseWriter, request *http.Request) {
		checkContext, cancel := context.WithTimeout(request.Context(), healthCheckTimeout)
		defer cancel()

		responseWriter.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if err := pinger.Ping(checkContext); err != nil {
			logger.Error("health check failed", "error", err)
			responseWriter.WriteHeader(http.StatusServiceUnavailable)
			_, _ = responseWriter.Write([]byte("database unavailable\n"))
			return
		}
		_, _ = responseWriter.Write([]byte("ok\n"))
	}
}
