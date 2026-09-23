package server

import (
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/go-chi/chi/v5"
)

// State holds the shared state for HTTP handlers.
//
// Index is swapped atomically by the reload path (see internal/server/reload):
// readers see either the old or the new *Index, never a half-built one.
// IMPORTANT: once an *apitypes.Index is published via SetIndex it MUST be
// treated as immutable — readers iterate it without locks and rely on
// atomic.Pointer.Load returning a stable pointer. Mutating a published
// index in place will trip the race detector. To replace, construct a
// fresh *apitypes.Index (e.g. via apitypes.ParseIndex) and Store it.
//
// SkillsIndex follows the same contract: published via SetSkillsIndex,
// readers MUST treat the loaded *skills.Index as immutable.
//
// SkillsDir is the per-process write coordinator for skills: it owns the
// mutex that serializes upload + delete + reload-mutating operations
// against dist/skills/. Read endpoints do NOT take this lock — they go
// through SkillsIndex() for race-free concurrent access.
type State struct {
	index       atomic.Pointer[apitypes.Index]
	skillsIndex atomic.Pointer[skills.Index]
	Dist        *repo.Dir
	SkillsDir   *SkillsDir
}

// Index returns the currently-published index, or nil if none has been
// loaded (initial startup race or a never-loaded misconfiguration).
func (s *State) Index() *apitypes.Index { return s.index.Load() }

// SetIndex atomically publishes a new index. The pointer is replaced
// in one step so in-flight requests see either the old or the new
// snapshot, never a torn read.
func (s *State) SetIndex(idx *apitypes.Index) { s.index.Store(idx) }

// SkillsIndex returns the currently-published skills index, or nil if
// none has been loaded (normal before the first Reload completes).
func (s *State) SkillsIndex() *skills.Index { return s.skillsIndex.Load() }

// SetSkillsIndex atomically publishes a new skills index, mirroring
// SetIndex for the agents collection.
func (s *State) SetSkillsIndex(idx *skills.Index) { s.skillsIndex.Store(idx) }

// NewState constructs a State with the given Dist, initial agent Index,
// and initial skills Index. Either index may be nil (a reload will
// populate it). dist must be non-nil.
func NewState(dist *repo.Dir, idx *apitypes.Index, sIdx *skills.Index) *State {
	s := &State{
		Dist:      dist,
		SkillsDir: NewSkillsDir(dist),
	}
	if idx != nil {
		s.SetIndex(idx)
	}
	if sIdx != nil {
		s.SetSkillsIndex(sIdx)
	}
	return s
}

// NewRouter wires up the marketplace-api HTTP routes.
func NewRouter(s *State, password string, logger *slog.Logger) http.Handler {
	r := chi.NewRouter()

	// Request logger covers everything except the /api/v1/health probe —
	// health probes hit on a tight interval from orchestrators and the
	// noise drowns out the interesting traffic if logged at INFO.
	if logger == nil {
		logger = slog.Default()
	}
	r.Use(requestLogger(logger))

	// Anonymous surfaces — no auth, no middleware.
	r.Get("/swagger", HandleSwaggerUI)
	r.Get("/swagger/openapi.json", HandleOpenAPISpec)

	r.Route("/api/v1", func(r chi.Router) {
		// Anonymous by router-group ordering. Do NOT move into the inner
		// group below — TestHealth_NoAuthRequired enforces that.
		r.Get("/health", HandleHealth)

		// All other endpoints require Basic Auth.
		r.Group(func(r chi.Router) {
			r.Use(BasicAuthMiddleware(password))
			r.Get("/index", HandleIndex(s))
			r.Get("/agents/{name}", HandleAgent(s))
			r.Get("/agents/{name}/{source}/{version}/manifest", HandleManifest(s))
			r.Get("/agents/{name}/{source}/{version}/tarball", HandleTarball(s))
			r.Get("/agents/{name}/{source}/{version}/sha256", HandleSHA256(s))
			r.Get("/agents/{name}/{source}/{version}/config-schema", HandleConfigSchema(s))
			r.Get("/index.json", HandleRawIndex(s))

			// Skills endpoints (Phase 3: read-only). Upload/delete routes
			// are added by Phase 6/7 alongside the write mutex.
			r.Get("/skills", HandleSkillsList(s))
			r.Get("/skills/{source}", HandleSkillSourceList(s))
			r.Get("/skills/{source}/{name}", HandleSkill(s))
			r.Get("/skills/{source}/{name}/{version}", HandleSkillVersion(s))
			r.Get("/skills/{source}/{name}/{version}/download", HandleSkillDownload(s))
			r.Get("/skills/{source}/{name}/{version}/sha256", HandleSkillSHA256(s))
			r.Get("/skills/{source}/{name}/{version}/SKILL.md", HandleSkillRawMD(s))
			r.Get("/skills-index.json", HandleRawSkillsIndex(s))

			// Phase 6: write endpoint. Multipart upload with 50 MiB cap,
			// serialized via SkillsDir.mu.
			r.Post("/skills", HandleSkillUpload(s))

			// Phase 7: delete. Two URL shapes — one version, or every
			// version under a (source, name) pair. See plan §4.3.
			r.Delete("/skills/{source}/{name}/{version}", HandleSkillDelete(s))
			r.Delete("/skills/{source}/{name}", HandleSkillDelete(s))
		})
	})

	return r
}

// requestLogger is a tiny middleware that emits one log line per response.
// Health-check requests are dropped to DEBUG level to avoid flooding the
// log when Docker / k8s probes every second.
func requestLogger(logger *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rw, r)
			level := slog.LevelInfo
			if rw.status >= 500 {
				level = slog.LevelError
			} else if rw.status >= 400 {
				level = slog.LevelWarn
			}
			// Health probes: downgrade to DEBUG to keep INFO stream clean.
			if r.URL.Path == "/api/v1/health" {
				level = slog.LevelDebug
			}
			logger.LogAttrs(r.Context(), level, "http",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Int("status", rw.status),
				slog.Int64("duration_ms", time.Since(start).Milliseconds()),
				slog.String("remote", r.RemoteAddr),
			)
		})
	}
}

type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (s *statusRecorder) WriteHeader(code int) {
	s.status = code
	s.ResponseWriter.WriteHeader(code)
}