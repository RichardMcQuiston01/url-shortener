package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
)

const bearerScheme string = "bearer"

// RequireAPIKey rejects requests that do not carry "Authorization: Bearer
// <apiKey>". Both sides are hashed with SHA-256 before a constant-time
// comparison so neither the key's content nor its length leaks through timing.
// The Authorization header is never logged.
func RequireAPIKey(apiKey string, logger *slog.Logger, next http.Handler) http.Handler {
	expectedHash := sha256.Sum256([]byte(apiKey))

	return http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		headerValue := request.Header.Get("Authorization")
		if headerValue == "" {
			rejectUnauthorized(responseWriter, logger, "missing Authorization header; send 'Authorization: Bearer <API_KEY>'")
			return
		}

		scheme, suppliedKey, found := strings.Cut(headerValue, " ")
		if !found || !strings.EqualFold(scheme, bearerScheme) || strings.TrimSpace(suppliedKey) == "" {
			rejectUnauthorized(responseWriter, logger, "Authorization header must look like 'Bearer <API_KEY>'")
			return
		}

		suppliedHash := sha256.Sum256([]byte(strings.TrimSpace(suppliedKey)))
		if subtle.ConstantTimeCompare(expectedHash[:], suppliedHash[:]) != 1 {
			rejectUnauthorized(responseWriter, logger, "invalid API key")
			return
		}

		next.ServeHTTP(responseWriter, request)
	})
}

func rejectUnauthorized(responseWriter http.ResponseWriter, logger *slog.Logger, message string) {
	responseWriter.Header().Set("WWW-Authenticate", "Bearer")
	writeError(responseWriter, logger, http.StatusUnauthorized, CodeUnauthorized, message)
}
