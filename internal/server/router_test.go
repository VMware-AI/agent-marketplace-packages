package server

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHealth_NoAuthRequired verifies that GET /api/v1/health is reachable
// WITHOUT any Authorization header. This is enforced by router-group
// ordering — do NOT move /health into the inner BasicAuthMiddleware group.
//
// This test is the single most important regression guard for the auth
// exemption: any future PR that re-arranges the router and accidentally
// moves /health behind auth will fail here immediately.
func TestHealth_NoAuthRequired(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	// Empty State is fine — /health does not touch State.
	s := &State{}
	h := NewRouter(s, "any-password", logger)

	// 1. Anonymous GET /api/v1/health must succeed.
	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/health (anonymous): got %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	body := rr.Body.String()
	if !strings.Contains(body, `"status":"ok"`) {
		t.Errorf("/api/v1/health body missing status:ok: %q", body)
	}

	// 2. Sanity: another authed endpoint on the same router must reject.
	req = httptest.NewRequest(http.MethodGet, "/api/v1/index", nil)
	rr = httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("/api/v1/index (anonymous): got %d, want 401; body=%q", rr.Code, rr.Body.String())
	}
}

// TestHealth_WithAuthHeader_StillWorks confirms the route accepts the
// header too (auth middleware is upstream of the handler — irrelevant here,
// but documenting that bad creds don't *break* /health).
func TestHealth_WithAuthHeader_StillWorks(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	s := &State{}
	h := NewRouter(s, "any-password", logger)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	req.SetBasicAuth("user", "any")
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("/api/v1/health (with auth): got %d, want 200", rr.Code)
	}
}