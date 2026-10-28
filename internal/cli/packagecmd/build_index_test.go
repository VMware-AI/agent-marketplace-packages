
package packagecmd

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
)

// tarGzWithConfig builds a minimal tarball containing manifest.json plus the
// named config-contract files, at the given tar-internal paths (so callers can
// exercise both the current flat layout and the legacy nested one).
func tarGzWithConfig(t *testing.T, files map[string]string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "testagent-upstream-1.0.0.tar.gz")
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, body := range files {
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

const testManifestJSON = `{"schema_version":"1.1","agent":"testagent","source":"upstream","version":"1.0.0","channel":"stable","configs":[]}`

// TestStageConfigFiles verifies config/schema.json + config/example.* are
// copied out of a tarball into dist/config/<stem>/ and reported in the index
// entry, including the example's format.
func TestStageConfigFiles(t *testing.T) {
	schemaBody := `{"type":"object"}`
	exampleBody := `{"opencode":{"model":"m"}}`
	tarball := tarGzWithConfig(t, map[string]string{
		"manifest.json":        testManifestJSON,
		"config/schema.json":   schemaBody,
		"config/example.json":  exampleBody,
	})
	outDir := t.TempDir()

	got, err := stageConfigFiles(tarball, "testagent-upstream-1.0.0.tar.gz", outDir)
	if err != nil {
		t.Fatalf("stageConfigFiles: %v", err)
	}
	if got == nil {
		t.Fatal("expected config entry, got nil")
	}
	if got.Status != apitypes.ConfigStatusOK {
		t.Errorf("status = %q, want %q", got.Status, apitypes.ConfigStatusOK)
	}
	if got.ExampleFormat != "json" {
		t.Errorf("example_format = %q, want json", got.ExampleFormat)
	}
	if got.Schema == "" || got.Example == "" {
		t.Fatalf("schema/example paths not populated: %+v", got)
	}

	// Files must exist on disk with the exact bytes from the tarball.
	dir := filepath.Join(outDir, agentConfigDirName, "testagent-upstream-1.0.0")
	for name, want := range map[string]string{"schema.json": schemaBody, "example.json": exampleBody} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		if string(b) != want {
			t.Errorf("%s = %q, want %q", name, b, want)
		}
	}
}

// TestStageConfigFiles_YAMLExample verifies a YAML example is reported as
// format "yaml" so a client can pick the right editor without sniffing.
func TestStageConfigFiles_YAMLExample(t *testing.T) {
	tarball := tarGzWithConfig(t, map[string]string{
		"manifest.json":      testManifestJSON,
		"config/schema.json": `{"type":"object"}`,
		"config/example.yaml": "hermes:\n  model:\n    provider: custom:litellm\n",
	})
	got, err := stageConfigFiles(tarball, "testagent-upstream-1.0.0.tar.gz", t.TempDir())
	if err != nil {
		t.Fatalf("stageConfigFiles: %v", err)
	}
	if got == nil || got.ExampleFormat != "yaml" {
		t.Fatalf("expected yaml example format, got %+v", got)
	}
}

// TestStageConfigFiles_NestedLayout verifies config files are still found when
// nested under <version>/ (the legacy tools/pack.sh tarball layout).
func TestStageConfigFiles_NestedLayout(t *testing.T) {
	tarball := tarGzWithConfig(t, map[string]string{
		"1.0.0/manifest.json":      testManifestJSON,
		"1.0.0/config/schema.json": `{"type":"object"}`,
		"1.0.0/config/example.json": `{"a":1}`,
	})
	got, err := stageConfigFiles(tarball, "testagent-upstream-1.0.0.tar.gz", t.TempDir())
	if err != nil {
		t.Fatalf("stageConfigFiles: %v", err)
	}
	if got == nil || got.Status != apitypes.ConfigStatusOK {
		t.Fatalf("nested layout not staged: %+v", got)
	}
}

// TestStageConfigFiles_NoConfigDir verifies a version shipping no config/
// directory yields (nil, nil) — a legitimate state, not an error. The
// platform then serves 404 no_config_* and the frontend falls back to raw-only.
func TestStageConfigFiles_NoConfigDir(t *testing.T) {
	tarball := tarGzWithConfig(t, map[string]string{"manifest.json": testManifestJSON})
	got, err := stageConfigFiles(tarball, "testagent-upstream-1.0.0.tar.gz", t.TempDir())
	if err != nil {
		t.Fatalf("no config/ dir must not be an error, got: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil config entry for a version with no config/, got %+v", got)
	}
}
