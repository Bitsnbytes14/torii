package gateway

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"net/http"
	"strconv"

	"github.com/Bitsnbytes14/torii/internal/tenant"
)

const apiKeyHeader = "X-API-Key"

type tenantCtxKey struct{}

func TenantFromContext(ctx context.Context) (*tenant.Tenant, bool) {
	t, ok := ctx.Value(tenantCtxKey{}).(*tenant.Tenant)
	return t, ok
}

// RequireTenant identifies which tenant (API client) is calling. It's a
// separate concern from RequireJWT, which identifies the end user: a tenant
// key says "this app may use the API", a JWT says "this person is booking".
func RequireTenant(cache *TenantCache, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			apiKey := r.Header.Get(apiKeyHeader)
			if apiKey == "" {
				writeError(w, http.StatusUnauthorized, "missing X-API-Key header")
				return
			}

			t, err := cache.Get(r.Context(), apiKey)
			if errors.Is(err, tenant.ErrNotFound) {
				writeError(w, http.StatusUnauthorized, "unknown API key")
				return
			}
			if err != nil {
				// 503 rather than 401: the key may be perfectly valid, we just
				// can't check it, and clients should retry rather than give up.
				logger.Error("tenant lookup failed", "error", err)
				writeError(w, http.StatusServiceUnavailable, "tenant lookup unavailable")
				return
			}

			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), tenantCtxKey{}, t)))
		})
	}
}

// RateLimit must run after RequireTenant. If Redis is unreachable it fails
// open: losing rate limiting briefly is better than the limiter's own
// dependency taking the whole API down. A billing-grade quota would choose
// the opposite.
func RateLimit(limiter *RateLimiter, logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			t, ok := TenantFromContext(r.Context())
			if !ok {
				writeError(w, http.StatusInternalServerError, "tenant not resolved")
				return
			}

			d, err := limiter.Allow(r.Context(), t.ID, t.RateLimitPerMinute)
			if err != nil {
				logger.Warn("rate limiter unavailable, failing open", "tenant_id", t.ID, "error", err)
				next.ServeHTTP(w, r)
				return
			}

			w.Header().Set("X-RateLimit-Limit", strconv.Itoa(d.Limit))
			w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(d.Remaining))
			if !d.Allowed {
				retryAfter := int(math.Ceil(d.RetryAfter.Seconds()))
				w.Header().Set("Retry-After", strconv.Itoa(retryAfter))
				writeJSON(w, http.StatusTooManyRequests, map[string]any{
					"error":               "rate limit exceeded",
					"limit_per_minute":    d.Limit,
					"retry_after_seconds": retryAfter,
				})
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
