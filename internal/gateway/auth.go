package gateway

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type devTokenClaims struct {
	Holder string `json:"holder"`
	jwt.RegisteredClaims
}

// DevTokenHandler mints an HMAC-signed JWT for a given holder name, with no
// password or identity check at all. This is a Phase 1 placeholder standing
// in for real OIDC login (Phase 2) — it exists purely so the demo frontend
// and Bruno collection have something to call to get past the auth
// middleware below. Do not carry this endpoint past Phase 1.
func DevTokenHandler(secret []byte, logger *slog.Logger) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Holder string `json:"holder"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Holder == "" {
			writeError(w, http.StatusBadRequest, "holder is required")
			return
		}

		now := time.Now()
		claims := devTokenClaims{
			Holder: req.Holder,
			RegisteredClaims: jwt.RegisteredClaims{
				IssuedAt:  jwt.NewNumericDate(now),
				ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
			},
		}
		token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
		signed, err := token.SignedString(secret)
		if err != nil {
			logger.Error("failed to sign dev token", "error", err)
			writeError(w, http.StatusInternalServerError, "failed to mint token")
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": signed})
	}
}

// RequireJWT validates an HMAC-signed bearer token on the wrapped handler.
// It only checks that the token is well-formed, correctly signed, and
// unexpired — there's no user store yet to check the holder against, so
// this is gatekeeping ("did you get a token"), not authorization ("are you
// allowed to book as this holder"). That distinction goes away once real
// auth lands in Phase 2.
func RequireJWT(secret []byte) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			tokenStr, ok := strings.CutPrefix(authHeader, "Bearer ")
			if !ok || tokenStr == "" {
				writeError(w, http.StatusUnauthorized, "missing bearer token")
				return
			}

			token, err := jwt.ParseWithClaims(tokenStr, &devTokenClaims{}, func(t *jwt.Token) (interface{}, error) {
				if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
					return nil, fmt.Errorf("unexpected signing method: %v", t.Header["alg"])
				}
				return secret, nil
			})
			if err != nil || !token.Valid {
				writeError(w, http.StatusUnauthorized, "invalid or expired token")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}
