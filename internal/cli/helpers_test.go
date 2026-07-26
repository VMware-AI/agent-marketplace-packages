package cli

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// makeTarGz builds an in-memory tar.gz containing files at the given
// (name → content) mapping. Each entry gets mode 0644.
func makeTarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatalf("write header %s: %v", name, err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatalf("write content %s: %v", name, err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip: %v", err)
	}
	return buf.Bytes()
}

// TestRunTarExtract_PullsRequestedFile ensures the helper used by
// `agentpkg uninstall` can fish a single file out of a tarball without
// materializing the whole archive (so uninstall doesn't need 60MB of
// disk to remove 200 bytes of install.sh).
func TestRunTarExtract_PullsRequestedFile(t *testing.T) {
	dir := t.TempDir()
	tgzPath := filepath.Join(dir, "fixture.tgz")
	if err := os.WriteFile(tgzPath, makeTarGz(t, map[string]string{
		"manifest.json": `{"agent":"a"}`,
		"install.sh":    "#!/bin/sh\necho install\n",
		"payload/bin/x": "binary-bytes",
		"uninstall.sh":  "#!/bin/sh\necho uninstall\n",
	}), 0644); err != nil {
		t.Fatal(err)
	}

	destDir := filepath.Join(dir, "out")
	if err := os.MkdirAll(destDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := runTarExtract(tgzPath, "uninstall.sh", destDir); err != nil {
		t.Fatalf("runTarExtract: %v", err)
	}

	got, err := os.ReadFile(filepath.Join(destDir, "uninstall.sh"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if !strings.Contains(string(got), "echo uninstall") {
		t.Errorf("unexpected contents: %q", string(got))
	}

	// And: requesting a file that isn't there should error out cleanly.
	if err := runTarExtract(tgzPath, "nonexistent.sh", destDir); err == nil {
		t.Error("expected error for missing file, got nil")
	}
}
