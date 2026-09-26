// Package controlplane is the admin API for gateway config. It writes tenant
// config to Redis and never talks to gateways directly — gateways pick up
// changes on their next cache refresh, so there's no push channel to keep
// in sync with however many replicas happen to be running.
package controlplane

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/Bitsnbytes14/torii/internal/tenant"
)

type Handler struct {
	store      *tenant.Store
	logger     *slog.Logger
	adminToken string
}

func NewHandler(store *tenant.Store, logger *slog.Logger, adminToken string) *Handler {
	return &Handler{store: store, logger: logger, adminToken: adminToken}
}

func (h *Handler) Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Group(func(r chi.Router) {
		r.Use(h.requireAdminToken)
		r.Post("/tenants", h.createTenant)
		r.Get("/tenants", h.listTenants)
		r.Patch("/tenants/{id}", h.updateTenant)
		r.Delete("/tenants/{id}", h.deleteTenant)
	})
	return r
}

// requireAdminToken guards every tenant route with one shared bearer token.
// GET /tenants returns every tenant's API key, so this is the only thing
// between a caller and every tenant's credentials. A single static token is
// the simplest thing that closes that hole; per-operator identity is what
// OIDC/RBAC adds later.
func (h *Handler) requireAdminToken(next http.Handler) http.Handler {
	// Comparing SHA-256 digests keeps the comparison constant-time even when
	// lengths differ; ConstantTimeCompare on the raw strings returns early on
	// a length mismatch and so leaks the token's length.
	want := sha256.Sum256([]byte(h.adminToken))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		gotSum := sha256.Sum256([]byte(got))
		if !ok || got == "" || subtle.ConstantTimeCompare(gotSum[:], want[:]) != 1 {
			writeError(w, http.StatusUnauthorized, "missing or invalid admin token")
			return
		}
		next.ServeHTTP(w, r)
	})
}

type createTenantRequest struct {
	Name               string `json:"name"`
	RateLimitPerMinute int    `json:"rate_limit_per_minute"`
}

// createTenantResponse is the only response that ever carries a plaintext
// API key. It's returned once, at creation; the store never persists the
// plaintext, so there is no "show it again" endpoint later.
type createTenantResponse struct {
	tenant.Tenant
	APIKey string `json:"api_key"`
	Note   string `json:"note"`
}

func (h *Handler) createTenant(w http.ResponseWriter, r *http.Request) {
	var req createTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.Name == "" {
		writeError(w, http.StatusBadRequest, "name is required")
		return
	}
	if req.RateLimitPerMinute <= 0 {
		writeError(w, http.StatusBadRequest, "rate_limit_per_minute must be greater than zero")
		return
	}

	t, apiKey, err := h.store.Create(r.Context(), req.Name, req.RateLimitPerMinute)
	if err != nil {
		h.logger.Error("create tenant failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to create tenant")
		return
	}
	h.logger.Info("tenant created", "tenant_id", t.ID, "rate_limit_per_minute", t.RateLimitPerMinute)
	writeJSON(w, http.StatusCreated, createTenantResponse{
		Tenant: *t,
		APIKey: apiKey,
		Note:   "this key is shown once and cannot be retrieved again; store it now",
	})
}

func (h *Handler) listTenants(w http.ResponseWriter, r *http.Request) {
	tenants, err := h.store.List(r.Context())
	if err != nil {
		h.logger.Error("list tenants failed", "error", err)
		writeError(w, http.StatusInternalServerError, "failed to list tenants")
		return
	}
	writeJSON(w, http.StatusOK, tenants)
}

// PATCH only accepts rate_limit_per_minute: the name is cosmetic and the API
// key is immutable (rotating it would be its own endpoint, since the old key
// has to stop working atomically).
type updateTenantRequest struct {
	RateLimitPerMinute *int `json:"rate_limit_per_minute"`
}

func (h *Handler) updateTenant(w http.ResponseWriter, r *http.Request) {
	var req updateTenantRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body")
		return
	}
	if req.RateLimitPerMinute == nil || *req.RateLimitPerMinute <= 0 {
		writeError(w, http.StatusBadRequest, "rate_limit_per_minute must be greater than zero")
		return
	}

	id := chi.URLParam(r, "id")
	t, err := h.store.UpdateRateLimit(r.Context(), id, *req.RateLimitPerMinute)
	if errors.Is(err, tenant.ErrNotFound) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		h.logger.Error("update tenant failed", "tenant_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to update tenant")
		return
	}
	h.logger.Info("tenant rate limit updated", "tenant_id", id, "rate_limit_per_minute", t.RateLimitPerMinute)
	writeJSON(w, http.StatusOK, t)
}

func (h *Handler) deleteTenant(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	err := h.store.Delete(r.Context(), id)
	if errors.Is(err, tenant.ErrNotFound) {
		writeError(w, http.StatusNotFound, "tenant not found")
		return
	}
	if err != nil {
		h.logger.Error("delete tenant failed", "tenant_id", id, "error", err)
		writeError(w, http.StatusInternalServerError, "failed to delete tenant")
		return
	}
	h.logger.Info("tenant deleted", "tenant_id", id)
	w.WriteHeader(http.StatusNoContent)
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
