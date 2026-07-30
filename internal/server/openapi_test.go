package server

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestOpenAPISpec_ServesJSON confirms /swagger/openapi.json returns a
// parseable OpenAPI 3.0 spec and includes every endpoint we ship.
func TestOpenAPISpec_ServesJSON(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRouter(&State{}, "any-password", logger)

	req := httptest.NewRequest(http.MethodGet, "/swagger/openapi.json", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Errorf("Content-Type: got %q", ct)
	}

	var spec map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &spec); err != nil {
		t.Fatalf("body is not JSON: %v\n%s", err, rr.Body.String())
	}
	if spec["openapi"] != "3.0.3" {
		t.Errorf("openapi version: got %v, want 3.0.3", spec["openapi"])
	}
	paths, _ := spec["paths"].(map[string]any)
	if _, ok := paths["/api/v1/health"]; !ok {
		t.Error("missing path /api/v1/health")
	}
	if _, ok := paths["/api/v1/index"]; !ok {
		t.Error("missing path /api/v1/index")
	}
}

// TestSwaggerUI_ServesHTML confirms /swagger returns HTML containing
// the Swagger UI loader.
func TestSwaggerUI_ServesHTML(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRouter(&State{}, "any-password", logger)

	req := httptest.NewRequest(http.MethodGet, "/swagger", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status: got %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type: got %q", ct)
	}
	if !strings.Contains(rr.Body.String(), "swagger-ui") {
		t.Errorf("body does not look like Swagger UI: %q", rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), `/swagger/openapi.json`) {
		t.Errorf("Swagger UI does not point at our /swagger/openapi.json: %q", rr.Body.String())
	}
}

// TestSwaggerUI_IsAnonymous confirms the swagger endpoints do not require
// Basic Auth (the same exemption as /api/v1/health).
func TestSwaggerUI_IsAnonymous(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	h := NewRouter(&State{}, "any-password", logger)

	for _, path := range []string{"/swagger", "/swagger/openapi.json"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, req)
		if rr.Code == http.StatusUnauthorized {
			t.Errorf("%s unexpectedly requires auth: %d", path, rr.Code)
		}
	}
}