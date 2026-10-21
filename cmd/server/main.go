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
	"path/filepath"
	"strconv"
	"syscall"
	"time"

	"github.com/richardmcquiston01/url-shortener/internal/admin"
	"github.com/richardmcquiston01/url-shortener/internal/backup"
	"github.com/richardmcquiston01/url-shortener/internal/config"
	"github.com/richardmcquiston01/url-shortener/internal/httpapi"
	"github.com/richardmcquiston01/url-shortener/internal/links"
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

	// Nightly backups run inside the app. Stopping them is deferred after the
	// database close above, so it runs first and no backup is cut off mid-copy.
	backupContext, stopBackups := context.WithCancel(signalContext)
	backupsStopped := make(chan struct{})
	go func() {
		defer close(backupsStopped)
		backup.NewScheduler(database, filepath.Join(filepath.Dir(appConfig.DatabasePath), "backups"), logger, backup.Options{}).
			Run(backupContext)
	}()
	defer func() {
		stopBackups()
		<-backupsStopped
	}()

	clickRecorder := redirect.NewBatchRecorder(database, logger, redirect.BatchRecorderOptions{})
	// Deferred after the database close above, so it runs first: queued clicks
	// are flushed while the database is still open.
	defer func() {
		flushContext, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer cancel()
		if closeErr := clickRecorder.Close(flushContext); closeErr != nil {
			logger.Error("flush clicks on shutdown", "error", closeErr)
		}
	}()

	validator, err := links.NewValidator(appConfig.BaseURL)
	if err != nil {
		return fmt.Errorf("build link validator from BASE_URL: %w", err)
	}
	linkService := links.NewService(database, validator, links.RandomSlugGenerator{})

	adminHandler, err := admin.NewHandler(admin.Options{
		AdminPassword: appConfig.AdminPassword,
		SessionSecret: appConfig.SessionSecret,
		BaseURL:       appConfig.BaseURL,
		TrustProxy:    appConfig.TrustProxy,
		Links:         linkService,
		Stats:         database,
		Logger:        logger,
	})
	if err != nil {
		return fmt.Errorf("build admin handler: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", redirect.HealthHandler(database, logger))
	redirect.NewHandler(database, clickRecorder, appConfig.HomeRedirectURL, logger).Register(mux)
	httpapi.NewHandler(linkService, database, appConfig.BaseURL, logger).
		Register(mux, appConfig.APIKey)
	if err := adminHandler.Register(mux); err != nil {
		return fmt.Errorf("register admin routes: %w", err)
	}

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
