package httpapi

import (
	"bytes"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testAPIKey string = "0123456789abcdef0123456789abcdef"

func discardLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func okHandler() http.Handler {
	return http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		_, _ = responseWriter.Write([]byte("reached"))
	})
}

func TestRequireAPIKey(t *testing.T) {
	testCases := []struct {
		name            string
		authorization   string
		expectedStatus  int
		expectedMessage string
	}{
		{"correct key", "Bearer " + testAPIKey, http.StatusOK, ""},
		{"scheme is case-insensitive", "bearer " + testAPIKey, http.StatusOK, ""},
		{"missing header", "", http.StatusUnauthorized, "missing Authorization header"},
		{"wrong key", "Bearer wrong-key", http.StatusUnauthorized, "invalid API key"},
		{"key with wrong length", "Bearer " + testAPIKey + "x", http.StatusUnauthorized, "invalid API key"},
		{"wrong scheme", "Basic " + testAPIKey, http.StatusUnauthorized, "must look like"},
		{"no key after scheme", "Bearer ", http.StatusUnauthorized, "must look like"},
		{"key without scheme", testAPIKey, http.StatusUnauthorized, "must look like"},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/links", nil)
			if testCase.authorization != "" {
				request.Header.Set("Authorization", testCase.authorization)
			}
			recorder := httptest.NewRecorder()
			RequireAPIKey(testAPIKey, discardLogger(), okHandler()).ServeHTTP(recorder, request)

			if recorder.Code != testCase.expectedStatus {
				t.Fatalf("status = %d, want %d", recorder.Code, testCase.expectedStatus)
			}
			if testCase.expectedStatus == http.StatusOK {
				return
			}
			var body ErrorBody
			if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
				t.Fatalf("body is not the JSON error shape: %v: %s", err, recorder.Body.String())
			}
			if body.Error != CodeUnauthorized || !strings.Contains(body.Message, testCase.expectedMessage) {
				t.Errorf("body = %+v, want unauthorized containing %q", body, testCase.expectedMessage)
			}
			if recorder.Header().Get("WWW-Authenticate") != "Bearer" {
				t.Errorf("WWW-Authenticate = %q, want Bearer", recorder.Header().Get("WWW-Authenticate"))
			}
			if recorder.Header().Get("Content-Type") != "application/json; charset=utf-8" {
				t.Errorf("Content-Type = %q", recorder.Header().Get("Content-Type"))
			}
		})
	}
}

func TestRequireAPIKeyNeverLogsTheKey(t *testing.T) {
	var logOutput bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&logOutput, nil))

	request := httptest.NewRequest(http.MethodGet, "/api/links", nil)
	request.Header.Set("Authorization", "Bearer secret-attempt-value")
	RequireAPIKey(testAPIKey, logger, okHandler()).ServeHTTP(httptest.NewRecorder(), request)

	if strings.Contains(logOutput.String(), "secret-attempt-value") || strings.Contains(logOutput.String(), testAPIKey) {
		t.Errorf("log output leaks a key: %s", logOutput.String())
	}
}
