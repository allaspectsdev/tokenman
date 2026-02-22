package proxy

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"strings"
)

// AuthMiddleware returns a chi-compatible middleware that validates a Bearer
// token using constant-time comparison. Requests without a valid token receive
// 401 (missing) or 403 (invalid). This mirrors the dashboard auth pattern in
// metrics/api.go.
func AuthMiddleware(token string) func(http.Handler) http.Handler {
	tokenBytes := []byte(token)
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				w.Header().Set("WWW-Authenticate", "Bearer")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
				return
			}

			const prefix = "Bearer "
			if !strings.HasPrefix(authHeader, prefix) {
				w.Header().Set("WWW-Authenticate", "Bearer")
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusUnauthorized)
				json.NewEncoder(w).Encode(map[string]string{"error": "authentication required"})
				return
			}

			provided := []byte(strings.TrimPrefix(authHeader, prefix))
			if subtle.ConstantTimeCompare(provided, tokenBytes) != 1 {
				w.Header().Set("Content-Type", "application/json; charset=utf-8")
				w.WriteHeader(http.StatusForbidden)
				json.NewEncoder(w).Encode(map[string]string{"error": "invalid token"})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}
