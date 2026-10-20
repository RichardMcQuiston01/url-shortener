package main

import (
	"log/slog"
	"net/http"
	"time"
)

// statusRecorder captures the status code written by a handler.
type statusRecorder struct {
	http.ResponseWriter
	statusCode int
}

func (recorder *statusRecorder) WriteHeader(statusCode int) {
	recorder.statusCode = statusCode
	recorder.ResponseWriter.WriteHeader(statusCode)
}

// withRequestLogging logs method, path (never the query string, which may hold
// secrets), status and duration. Headers and cookies are never logged.
func withRequestLogging(logger *slog.Logger, next http.Handler) http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		recorder := &statusRecorder{ResponseWriter: responseWriter, statusCode: http.StatusOK}
		next.ServeHTTP(recorder, request)
		logger.Info("request",
			"method", request.Method,
			"path", request.URL.Path,
			"status", recorder.statusCode,
			"duration_ms", time.Since(startedAt).Milliseconds(),
		)
	})
}
