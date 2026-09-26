package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bitsnbytes14/torii/internal/controlplane"
	"github.com/Bitsnbytes14/torii/internal/startup"
	"github.com/Bitsnbytes14/torii/internal/tenant"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))
	port := getEnv("PORT", "8082")
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379/0")

	// No default on purpose: a fallback token would be a known credential
	// guarding every tenant's API key, so refusing to start is safer.
	adminToken := os.Getenv("CONTROLPLANE_ADMIN_TOKEN")
	if adminToken == "" {
		logger.Error("CONTROLPLANE_ADMIN_TOKEN must be set")
		os.Exit(1)
	}

	opts, err := redis.ParseURL(redisURL)
	if err != nil {
		logger.Error("invalid REDIS_URL", "error", err)
		os.Exit(1)
	}
	rdb := redis.NewClient(opts)
	defer rdb.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := startup.WaitFor(ctx, logger, "redis", func(ctx context.Context) error {
		return rdb.Ping(ctx).Err()
	}); err != nil {
		logger.Error("failed to reach redis", "error", err)
		os.Exit(1)
	}

	handler := controlplane.NewHandler(tenant.NewStore(rdb), logger, adminToken)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: handler.Routes(),
	}

	go func() {
		logger.Info("controlplane listening", "port", port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server error", "error", err)
			os.Exit(1)
		}
	}()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	<-stop

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()
	logger.Info("shutting down controlplane")
	_ = srv.Shutdown(shutdownCtx)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
