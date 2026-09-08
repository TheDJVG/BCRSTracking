package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TheDJVG/BCRSTracking/internal/client"
	"github.com/TheDJVG/BCRSTracking/internal/config"
	"github.com/TheDJVG/BCRSTracking/internal/metrics"
	"github.com/TheDJVG/BCRSTracking/internal/poller"
	"github.com/TheDJVG/BCRSTracking/internal/storage"
)

func main() {
	var logLevel slog.LevelVar
	logLevel.Set(slog.LevelInfo)

	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: &logLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting BCRS tracking service...")

	cfg, err := config.Load()
	if err != nil {
		logger.Error("configuration initialization failed", "error", err)
		os.Exit(1)
	}

	switch strings.ToLower(cfg.LogLevel) {
	case "debug":
		logLevel.Set(slog.LevelDebug)
	case "warn", "warning":
		logLevel.Set(slog.LevelWarn)
	case "error":
		logLevel.Set(slog.LevelError)
	default:
		logLevel.Set(slog.LevelInfo)
	}

	m := metrics.New()

	// Metrics & Health Server
	metricsMux := http.NewServeMux()
	metricsMux.Handle("/metrics", m.Handler())
	metricsMux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("OK"))
	})

	metricsServer := &http.Server{
		Addr:         fmt.Sprintf(":%d", cfg.MetricsPort),
		Handler:      metricsMux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		logger.Info("metrics and health server listening", "port", cfg.MetricsPort)
		if err := metricsServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("metrics server crashed", "error", err)
		}
	}()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Initialize Storage
	var store *storage.Storage
	// Retry loop for ClickHouse readiness
	for i := 0; i < 10; i++ {
		store, err = storage.New(ctx, cfg, m, logger)
		if err == nil {
			break
		}
		logger.Warn("waiting for ClickHouse connection...", "attempt", i+1, "error", err)
		select {
		case <-ctx.Done():
			logger.Error("context canceled during ClickHouse connection attempts")
			os.Exit(1)
		case <-time.After(3 * time.Second):
		}
	}
	if err != nil {
		logger.Error("failed to connect to ClickHouse", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.InitSchema(ctx); err != nil {
		logger.Error("failed to initialize ClickHouse tables", "error", err)
		os.Exit(1)
	}
	logger.Info("ClickHouse tables verified and initialized")

	bcrsClient := client.New(cfg, m, client.WithLogger(logger))
	pollService := poller.New(cfg, bcrsClient, store, m, logger)

	go pollService.Start(ctx)

	// Graceful Shutdown Handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	sig := <-sigChan

	logger.Info("shutdown signal received", "signal", sig.String())
	cancel()

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer shutdownCancel()

	if err := metricsServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("metrics server shutdown error", "error", err)
	}

	logger.Info("BCRS tracker shutdown completed cleanly")
}
