package cli

import (
	"os"
	"path/filepath"
	"testing"
)

// TestExtractRenderScriptFromTarball_BothLayouts verifies that the
// tarball extractor finds render-config.sh under either the version
// prefix or the tarball root.
func TestExtractRenderScriptFromTarball_BothLayouts(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "fake.tar.gz")
	scriptContent := "#!/usr/bin/env bash\necho render-config\n"
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"0.0.55/render-config.sh": scriptContent,
	}), 0644); err != nil {
		t.Fatalf("write nested tarball: %v", err)
	}

	// Layout 1: <version>/render-config.sh
	out1 := filepath.Join(dir, "out1.sh")
	if err := extractRenderScriptFromTarball(tarball, "0.0.55", out1); err != nil {
		t.Fatalf("extract from nested layout: %v", err)
	}
	got, err := os.ReadFile(out1)
	if err != nil {
		t.Fatalf("read out1: %v", err)
	}
	if string(got) != scriptContent {
		t.Errorf("nested layout content: got %q want %q", got, scriptContent)
	}
	if info, err := os.Stat(out1); err != nil {
		t.Fatalf("stat out1: %v", err)
	} else if info.Mode().Perm() != 0755 {
		t.Errorf("render script mode: got %o want 0755", info.Mode().Perm())
	}

	// Layout 2: render-config.sh at root
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"render-config.sh": scriptContent,
	}), 0644); err != nil {
		t.Fatalf("write root tarball: %v", err)
	}
	out2 := filepath.Join(dir, "out2.sh")
	if err := extractRenderScriptFromTarball(tarball, "0.0.55", out2); err != nil {
		t.Fatalf("extract from root layout: %v", err)
	}
}

// TestExtractManifestFromTarball_BothLayouts verifies that the manifest
// extractor finds manifest.json in either layout.
func TestExtractManifestFromTarball_BothLayouts(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "fake.tar.gz")
	const manifestContent = `{"schema_version":"1.1","agent":"opencode","source":"upstream","version":"0.0.55","channel":"stable","requires":{},"payload":[],"checksums":{},"services":[],"configs":[]}`

	// Layout 1: nested
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"0.0.55/manifest.json": manifestContent,
	}), 0644); err != nil {
		t.Fatalf("write nested tarball: %v", err)
	}
	mf, err := extractManifestFromTarball(tarball, "0.0.55")
	if err != nil {
		t.Fatalf("extract nested: %v", err)
	}
	if mf.Agent != "opencode" || mf.Version != "0.0.55" {
		t.Errorf("nested manifest: got agent=%q version=%q", mf.Agent, mf.Version)
	}
	if len(mf.Services) != 0 || len(mf.Configs) != 0 {
		t.Errorf("nested manifest: got %d services, %d configs", len(mf.Services), len(mf.Configs))
	}

	// Layout 2: root
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"manifest.json": manifestContent,
	}), 0644); err != nil {
		t.Fatalf("write root tarball: %v", err)
	}
	mf2, err := extractManifestFromTarball(tarball, "0.0.55")
	if err != nil {
		t.Fatalf("extract root: %v", err)
	}
	if mf2.Agent != "opencode" {
		t.Errorf("root manifest: got agent=%q", mf2.Agent)
	}
}

// TestExtractManifestFromTarball_NotFound verifies the error message when
// no manifest exists.
func TestExtractManifestFromTarball_NotFound(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "empty.tar.gz")
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"random.txt": "no manifest here",
	}), 0644); err != nil {
		t.Fatalf("write empty tarball: %v", err)
	}
	if _, err := extractManifestFromTarball(tarball, "0.0.55"); err == nil {
		t.Errorf("expected error when no manifest in tarball, got nil")
	}
}