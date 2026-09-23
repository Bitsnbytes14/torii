// Package controlplane is a stub for now — just enough structure that
// Phase 3 (dynamic gateway config over Redis) has somewhere to grow into
// without a rewrite. No config storage or gateway wiring yet.
package controlplane

import (
	"net/http"

	"github.com/go-chi/chi/v5"
)

func Routes() chi.Router {
	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	return r
}
