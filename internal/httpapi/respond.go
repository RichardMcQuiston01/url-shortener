package httpapi

import (
	"encoding/json"
	"log/slog"
	"net/http"
)

// writeJSON writes value as a JSON response with the given status. An encoding
// failure can only be logged: the status line has already been sent.
func writeJSON(responseWriter http.ResponseWriter, logger *slog.Logger, status int, value any) {
	responseWriter.Header().Set("Content-Type", "application/json; charset=utf-8")
	responseWriter.WriteHeader(status)
	encoder := json.NewEncoder(responseWriter)
	encoder.SetEscapeHTML(false) // keep <API_KEY> readable in messages
	if err := encoder.Encode(value); err != nil {
		logger.Error("encode JSON response", "status", status, "error", err)
	}
}

// writeError writes the standard error body: {"error": code, "message": ...}.
// For CodeInternalError, pass a generic message and log the detail instead.
func writeError(responseWriter http.ResponseWriter, logger *slog.Logger, status int, code ErrorCode, message string) {
	writeJSON(responseWriter, logger, status, ErrorBody{Error: code, Message: message})
}
