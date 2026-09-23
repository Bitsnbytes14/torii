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

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"github.com/Bitsnbytes14/torii/internal/gateway"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	port := getEnv("PORT", "8080")
	bookingServiceURL := getEnv("BOOKING_SERVICE_URL", "http://localhost:8081")
	webDir := getEnv("WEB_DIR", "./web")
	jwtSecret := []byte(getEnv("JWT_SECRET", "dev-secret-change-me"))

	target, err := url.Parse(bookingServiceURL)
	if err != nil {
		logger.Error("invalid BOOKING_SERVICE_URL", "error", err)
		os.Exit(1)
	}
	proxy := gateway.NewBookingProxy(target)

	r := chi.NewRouter()
	r.Use(gateway.RequestLogger(logger))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization"},
		AllowCredentials: false,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Post("/auth/dev-token", gateway.DevTokenHandler(jwtSecret, logger))

	r.Route("/api", func(api chi.Router) {
		// POST /bookings is the only write path in Phase 1, so it's the
		// only route gated behind the JWT middleware; GETs stay public so
		// the demo frontend can list events/bookings without a login step.
		api.With(gateway.RequireJWT(jwtSecret)).Post("/bookings", proxy.ServeHTTP)
		api.Handle("/*", proxy)
	})

	fileServer := http.FileServer(http.Dir(webDir))
	r.Handle("/*", fileServer)

	srv := &http.Server{
		Addr:    ":" + port,
		Handler: r,
	}

	go func() {
		logger.Info("gateway listening", "port", port, "booking_service_url", bookingServiceURL)
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
