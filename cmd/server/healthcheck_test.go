package main

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func portOf(t *testing.T, server *httptest.Server) string {
	t.Helper()
	parsed, err := url.Parse(server.URL)
	if err != nil {
		t.Fatalf("parse %q: %v", server.URL, err)
	}
	return parsed.Port()
}

func TestCheckHealth(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/healthz" {
			http.NotFound(responseWriter, request)
			return
		}
		_, _ = responseWriter.Write([]byte("ok\n"))
	}))
	defer healthy.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(responseWriter http.ResponseWriter, _ *http.Request) {
		http.Error(responseWriter, "database unavailable", http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()

	client := &http.Client{Timeout: time.Second}
	if err := checkHealth(portOf(t, healthy), client); err != nil {
		t.Errorf("healthy server: %v", err)
	}
	if err := checkHealth(portOf(t, unhealthy), client); err == nil || !strings.Contains(err.Error(), "503") {
		t.Errorf("a 503 must fail the check and name the status, got %v", err)
	}
}

func TestCheckHealthFailsWhenNothingIsListening(t *testing.T) {
	closed := httptest.NewServer(http.NotFoundHandler())
	port := portOf(t, closed)
	closed.Close()

	if err := checkHealth(port, &http.Client{Timeout: time.Second}); err == nil {
		t.Error("expected an error when the server is not running")
	}
}
