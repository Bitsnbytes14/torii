package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/Bitsnbytes14/torii/internal/booking"
	"github.com/Bitsnbytes14/torii/internal/startup"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	dsn := getEnv("DATABASE_URL", "postgres://torii:torii@localhost:5432/torii?sslmode=disable")
	port := getEnv("PORT", "8083")

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		logger.Error("failed to create database pool", "error", err)
		os.Exit(1)
	}
	defer pool.Close()

	if err := startup.WaitFor(ctx, logger, "postgres", pool.Ping); err != nil {
		logger.Error("failed to reach database", "error", err)
		os.Exit(1)
	}

	store := booking.NewStore(pool)
	if err := store.Migrate(ctx); err != nil {
		logger.Error("failed to migrate database", "error", err)
		os.Exit(1)
	}

	handler := booking.NewHandler(store, logger)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: handler.Routes(),
	}

	go func() {
		logger.Info("booking service listening", "port", port)
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
	logger.Info("shutting down booking service")
	_ = srv.Shutdown(shutdownCtx)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
