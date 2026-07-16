package server

import (
	"net/http"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/go-chi/chi/v5"
)

// State holds the shared state for HTTP handlers — currently just the
// loaded index, which is set at startup and refreshed via SIGHUP (TODO).
//
// The state is read-only after startup; reload.go creates a new *State
// and atomically swaps it.
type State struct {
	Index *apitypes.Index
	Dist  *repo.Dir
}

// NewRouter wires up the marketplace-api HTTP routes.
func NewRouter(s *State, password string) http.Handler {
	r := chi.NewRouter()

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", HandleHealth)

		// All other endpoints require Basic Auth.
		r.Group(func(r chi.Router) {
			r.Use(BasicAuthMiddleware(password))
			r.Get("/index", HandleIndex(s))
			r.Get("/agents/{name}", HandleAgent(s))
			r.Get("/agents/{name}/{source}/{version}/manifest", HandleManifest(s))
			r.Get("/agents/{name}/{source}/{version}/tarball", HandleTarball(s))
			r.Get("/agents/{name}/{source}/{version}/sha256", HandleSHA256(s))
			r.Get("/index.json", HandleRawIndex(s))
		})
	})

	return r
}