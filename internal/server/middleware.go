// Package server implements the marketplace-api HTTP server.
//
// Endpoints (see docs/api-reference.md for full spec):
//
//	GET /api/v1/health              (no auth)
//	GET /api/v1/index               (auth) — list all packages, manifests stripped
//	GET /api/v1/agents/:name        (auth) — list versions for one agent
//	GET /api/v1/agents/:n/:s/:v/manifest   (auth) — full technical manifest
//	GET /api/v1/agents/:n/:s/:v/tarball    (auth) — stream tarball bytes
//	GET /api/v1/agents/:n/:s/:v/sha256     (auth) — sha256 sidecar text
//	GET /api/v1/index.json           (auth) — raw dist/index.json (debug)
//
// Authentication: HTTP Basic Auth, single shared password from MARKETPLACE_API_PASSWORD env var.
package server

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"net/http"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
)

// ctxKey is used to pass per-request values through context.Context.
type ctxKey int

const (
	ctxKeyUser ctxKey = iota
)

// userInfo represents the authenticated caller (just a placeholder —
// Basic Auth here uses a single shared password, no per-user identity).
type userInfo struct {
	Name string
}

// BasicAuthMiddleware validates the HTTP Basic Auth header against the
// configured password. On failure it writes a 401 with the standard
// error shape and aborts the request.
func BasicAuthMiddleware(password string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, pass, ok := r.BasicAuth()
			if !ok {
				writeAuthRequired(w)
				return
			}
			if subtle.ConstantTimeCompare([]byte(pass), []byte(password)) != 1 {
				writeAuthRequired(w)
				return
			}
			if user == "" {
				user = "marketplace"
			}
			ctx := context.WithValue(r.Context(), ctxKeyUser, &userInfo{Name: user})
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func writeAuthRequired(w http.ResponseWriter) {
	w.Header().Set("WWW-Authenticate", `Basic realm="marketplace-api"`)
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(apitypes.NewError("unauthorized", "invalid or missing credentials"))
}