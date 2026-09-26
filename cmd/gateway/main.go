package main

import (
	"context"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/Bitsnbytes14/torii/internal/gateway"
	"github.com/Bitsnbytes14/torii/internal/startup"
	"github.com/Bitsnbytes14/torii/internal/tenant"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	port := getEnv("PORT", "8080")
	bookingServiceURL := getEnv("BOOKING_SERVICE_URL", "http://localhost:8083")
	webDir := getEnv("WEB_DIR", "./web")
	jwtSecret := []byte(getEnv("JWT_SECRET", "dev-secret-change-me"))
	redisURL := getEnv("REDIS_URL", "redis://localhost:6379/0")

	cacheTTL, err := time.ParseDuration(getEnv("TENANT_CACHE_TTL", "5s"))
	if err != nil {
		logger.Error("invalid TENANT_CACHE_TTL", "error", err)
		os.Exit(1)
	}

	target, err := url.Parse(bookingServiceURL)
	if err != nil {
		logger.Error("invalid BOOKING_SERVICE_URL", "error", err)
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

	handler := gateway.NewRouter(gateway.Config{
		BookingURL: target,
		WebDir:     webDir,
		JWTSecret:  jwtSecret,
		Tenants:    gateway.NewTenantCache(tenant.NewStore(rdb), cacheTTL, logger),
		Limiter:    gateway.NewRateLimiter(rdb),
		Logger:     logger,
	})

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: handler,
	}

	// Hostname distinguishes replicas in logs: under compose it's the
	// container name, so gateway-a and gateway-b lines are easy to tell apart.
	hostname, _ := os.Hostname()

	go func() {
		logger.Info("gateway listening",
			"port", port,
			"instance", hostname,
			"booking_service_url", bookingServiceURL,
			"tenant_cache_ttl", cacheTTL.String(),
		)
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
	logger.Info("shutting down gateway")
	_ = srv.Shutdown(shutdownCtx)
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
