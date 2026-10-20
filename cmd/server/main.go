// Command server runs the URL shortener.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/config"
	"github.com/richardmcquiston01/url-shortener/internal/redirect"
	"github.com/richardmcquiston01/url-shortener/internal/store"
)

const (
	readHeaderTimeout time.Duration = 5 * time.Second
	readTimeout       time.Duration = 10 * time.Second
	writeTimeout      time.Duration = 15 * time.Second
	idleTimeout       time.Duration = 60 * time.Second
	shutdownTimeout   time.Duration = 15 * time.Second
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	if err := run(logger); err != nil {
		logger.Error("server stopped with an error", "error", err)
		os.Exit(1)
	}
}

// run wires everything together and blocks until SIGINT or SIGTERM, then
// shuts down gracefully.
func run(logger *slog.Logger) error {
	appConfig, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	signalContext, stopSignals := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stopSignals()

	database, err := store.Open(signalContext, appConfig.DatabasePath)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := database.Close(); closeErr != nil {
			logger.Error("close database", "error", closeErr)
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", redirect.HealthHandler(database, logger))

	httpServer := &http.Server{
		Addr:              net.JoinHostPort("", strconv.Itoa(appConfig.Port)),
		Handler:           withRequestLogging(logger, mux),
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	serverErrors := make(chan error, 1)
	go func() {
		logger.Info("listening", "addr", httpServer.Addr)
		if listenErr := httpServer.ListenAndServe(); listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			serverErrors <- fmt.Errorf("listen on %s: %w", httpServer.Addr, listenErr)
		}
		close(serverErrors)
	}()

	select {
	case err := <-serverErrors:
		return err
	case <-signalContext.Done():
		logger.Info("shutting down")
	}

	shutdownContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return nil
}
