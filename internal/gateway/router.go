package gateway

import (
	"log/slog"
	"net/http"
	"net/url"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"
)

type Config struct {
	BookingURL *url.URL
	WebDir     string
	JWTSecret  []byte
	Tenants    *TenantCache
	Limiter    *RateLimiter
	Logger     *slog.Logger
}

// NewRouter builds the whole gateway as a plain http.Handler, so tests can
// stand up several independent instances in one process and prove that
// shared behavior (rate limits) comes from Redis, not from shared memory.
func NewRouter(cfg Config) http.Handler {
	proxy := NewBookingProxy(cfg.BookingURL)

	r := chi.NewRouter()
	r.Use(RequestLogger(cfg.Logger))
	r.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Content-Type", "Authorization", apiKeyHeader},
		ExposedHeaders:   []string{"Retry-After", "X-RateLimit-Limit", "X-RateLimit-Remaining"},
		AllowCredentials: false,
	}))

	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	r.Post("/auth/dev-token", DevTokenHandler(cfg.JWTSecret, cfg.Logger))

	r.Route("/api", func(api chi.Router) {
		// Tenant auth and rate limiting run before JWT so that requests with
		// bad bearer tokens still count against the tenant's quota; otherwise
		// the limiter would do nothing to slow down token guessing.
		api.Use(RequireTenant(cfg.Tenants, cfg.Logger))
		api.Use(RateLimit(cfg.Limiter, cfg.Logger))

		// GETs need a tenant key but no user token, so the demo frontend can
		// list events/bookings without a login step.
		api.With(RequireJWT(cfg.JWTSecret)).Post("/bookings", proxy.ServeHTTP)
		api.Handle("/*", proxy)
	})

	r.Handle("/*", http.FileServer(http.Dir(cfg.WebDir)))
	return r
}
