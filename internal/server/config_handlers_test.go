package server

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
)

// configFixture builds a dist/ containing one staged version's config-contract
// files (as `agentpkg package build` would stage them), plus an in-memory index
// pointing at them. cfg is nil for a version that ships no config/ directory.
func configFixture(t *testing.T, cfg *apitypes.ConfigFiles, schemaBody, exampleBody string) *State {
	t.Helper()
	dist := t.TempDir()
	tarName := "testagent-upstream-1.0.0.tar.gz"

	idx := &apitypes.Index{
		GeneratedAt: "2026-01-01T00:00:00Z",
		Schema:      apitypes.SchemaVersion,
		Agents: []apitypes.Agent{{
			Name:     "testagent",
			Versions: []apitypes.Version{{Version: "1.0.0", Source: "upstream", Channel: "stable"}},
		}},
	}
	v := &idx.Agents[0].Versions[0]
	v.Tarball = apitypes.Tarball{Filename: tarName, SHA256: "sha256:deadbeef"}

	if cfg != nil {
		v.Config = cfg
		d := repo.NewDir(dist)
		dir := d.ConfigVersionPath(tarName)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if schemaBody != "" && cfg.Schema != "" {
			if err := os.WriteFile(filepath.Join(dir, "schema.json"), []byte(schemaBody), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if exampleBody != "" && cfg.Example != "" {
			name := filepath.Base(cfg.Example)
			if err := os.WriteFile(filepath.Join(dir, name), []byte(exampleBody), 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}

	s := &State{Dist: repo.NewDir(dist)}
	s.SetIndex(idx)
	return s
}

// TestHandleConfigSchemaFile_OK verifies the schema endpoint serves the staged
// schema.json byte-for-byte as application/json.
func TestHandleConfigSchemaFile_OK(t *testing.T) {
	body := `{"$schema":"http://json-schema.org/draft-07/schema#","type":"object"}`
	s := configFixture(t, &apitypes.ConfigFiles{
		Schema: "config/testagent-upstream-1.0.0/schema.json",
		Status: apitypes.ConfigStatusOK,
	}, body, "")

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil).WithContext(chiCtx(map[string]string{
		"name": "testagent", "source": "upstream", "version": "1.0.0",
	}))
	HandleConfigSchemaFile(s)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rec.Body.String() != body {
		t.Errorf("body = %q, want %q (must be served verbatim)", rec.Body.String(), body)
	}
}

// TestHandleConfigExampleFile_YAML verifies a YAML example is served as
// application/yaml and unchanged — the raw editor renders it as-is.
func TestHandleConfigExampleFile_YAML(t *testing.T) {
	body := "hermes:\n  model:\n    provider: custom:litellm\n---\nhermes-env:\n  HERMES_QUIET: false\n"
	s := configFixture(t, &apitypes.ConfigFiles{
		Schema:        "config/testagent-upstream-1.0.0/schema.json",
		Example:       "config/testagent-upstream-1.0.0/example.yaml",
		ExampleFormat: "yaml",
		Status:        apitypes.ConfigStatusOK,
	}, "{}", body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil).WithContext(chiCtx(map[string]string{
		"name": "testagent", "source": "upstream", "version": "1.0.0",
	}))
	HandleConfigExampleFile(s)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/yaml" {
		t.Errorf("Content-Type = %q, want application/yaml", ct)
	}
	if rec.Body.String() != body {
		t.Errorf("body = %q, want %q (must be served verbatim)", rec.Body.String(), body)
	}
}

// TestHandleConfigExampleFile_JSON verifies a JSON example is served as
// application/json.
func TestHandleConfigExampleFile_JSON(t *testing.T) {
	body := `{"opencode":{"model":"litellm/m"}}`
	s := configFixture(t, &apitypes.ConfigFiles{
		Schema:        "config/testagent-upstream-1.0.0/schema.json",
		Example:       "config/testagent-upstream-1.0.0/example.json",
		ExampleFormat: "json",
		Status:        apitypes.ConfigStatusOK,
	}, "{}", body)

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil).WithContext(chiCtx(map[string]string{
		"name": "testagent", "source": "upstream", "version": "1.0.0",
	}))
	HandleConfigExampleFile(s)(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if rec.Body.String() != body {
		t.Errorf("body = %q, want %q", rec.Body.String(), body)
	}
}

// TestConfigEndpoints_NoConfigDirectory verifies a version that ships no
// config/ directory gets a 404 with a distinct error code (not a 500, and not
// the generic "not found" for an unknown version) so a client can tell the two
// apart and fall back to a raw editor.
func TestConfigEndpoints_NoConfigDirectory(t *testing.T) {
	s := configFixture(t, nil, "", "")

	for _, tc := range []struct {
		name    string
		handler func(*State) http.HandlerFunc
	}{
		{"schema", HandleConfigSchemaFile},
		{"example", HandleConfigExampleFile},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			req := httptest.NewRequest("GET", "/x", nil).WithContext(chiCtx(map[string]string{
				"name": "testagent", "source": "upstream", "version": "1.0.0",
			}))
			tc.handler(s)(rec, req)

			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
			}
			wantCode := "no_config_schema"
			if tc.name == "example" {
				wantCode = "no_config_example"
			}
			if !contains(rec.Body.String(), wantCode) {
				t.Errorf("body %s does not mention error code %q", rec.Body.String(), wantCode)
			}
			if !contains(rec.Body.String(), apitypes.ConfigStatusMissing) {
				t.Errorf("body %s does not report status %q", rec.Body.String(), apitypes.ConfigStatusMissing)
			}
		})
	}
}

// TestConfigEndpoints_UnknownVersion verifies an unknown version yields the
// generic not_found, distinct from no_config_*.
func TestConfigEndpoints_UnknownVersion(t *testing.T) {
	s := configFixture(t, nil, "", "")
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/x", nil).WithContext(chiCtx(map[string]string{
		"name": "testagent", "source": "upstream", "version": "9.9.9",
	}))
	HandleConfigSchemaFile(s)(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if !contains(rec.Body.String(), "not_found") {
		t.Errorf("body %s should carry the generic not_found code", rec.Body.String())
	}
}

func contains(haystack, needle string) bool {
	return len(haystack) >= len(needle) && (func() bool {
		for i := 0; i+len(needle) <= len(haystack); i++ {
			if haystack[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	})()
}