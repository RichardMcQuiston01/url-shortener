package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

const healthcheckTimeout time.Duration = 3 * time.Second

// runHealthcheck is the `server healthcheck` subcommand used by the Docker
// HEALTHCHECK (the minimal image has no curl or wget). It asks the local server
// for /healthz and returns the process exit code.
func runHealthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}
	if err := checkHealth(port, &http.Client{Timeout: healthcheckTimeout}); err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck failed:", err)
		return 1
	}
	return 0
}

// checkHealth returns nil when GET /healthz on the loopback address answers 200.
func checkHealth(port string, client *http.Client) error {
	address := "http://" + net.JoinHostPort("127.0.0.1", port) + "/healthz"
	response, err := client.Get(address)
	if err != nil {
		return fmt.Errorf("request %s: %w", address, err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("%s answered %s, want 200 OK", address, response.Status)
	}
	return nil
}
