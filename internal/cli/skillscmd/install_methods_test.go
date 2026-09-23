package skillscmd

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// makeZipWithFiles builds an in-memory zip with the given (name, content)
// entries. Used by the install-method tests to stage fixtures.
func makeZipWithFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// makeTarGzWithFiles builds a .tar.gz in memory with the given entries.
// Used for tarball install_method tests.
func makeTarGzWithFiles(t *testing.T, files map[string]string) []byte {
	t.Helper()
	buf := &bytes.Buffer{}
	gz := gzip.NewWriter(buf)
	tw := tar.NewWriter(gz)
	for name, content := range files {
		hdr := &tar.Header{
			Name: name,
			Mode: 0o644,
			Size: int64(len(content)),
		}
		if err := tw.WriteHeader(hdr); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestResolveTargets_AllOnly(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"all"},
		InstallMethod: "zip-extract",
	}
	targets, err := ResolveTargets(sv, "foo", "/tmp/skills", filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	defer CleanupStaging(targets)
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1", len(targets))
	}
	if targets[0].Agent != "all" {
		t.Errorf("Agent = %q, want all", targets[0].Agent)
	}
	if targets[0].FinalPath != "/tmp/skills/foo" {
		t.Errorf("FinalPath = %q, want /tmp/skills/foo", targets[0].FinalPath)
	}
}

func TestResolveTargets_OpenCodeDefault(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"opencode"},
		InstallMethod: "zip-extract",
	}
	targets, err := ResolveTargets(sv, "foo", "", filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	defer CleanupStaging(targets)
	if len(targets) != 1 || targets[0].Agent != "opencode" {
		t.Fatalf("unexpected targets: %+v", targets)
	}
	home, _ := os.UserHomeDir()
	want := filepath.Join(home, ".config", "opencode", "skills", "foo")
	if targets[0].FinalPath != want {
		t.Errorf("FinalPath = %q, want %q", targets[0].FinalPath, want)
	}
}

func TestResolveTargets_OpenCodeOverride(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"opencode"},
		InstallMethod: "zip-extract",
		InstallPaths: map[string]string{
			"opencode": "/custom/path/$NAME",
		},
	}
	targets, err := ResolveTargets(sv, "foo", "", filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	defer CleanupStaging(targets)
	if targets[0].FinalPath != "/custom/path/foo" {
		t.Errorf("FinalPath = %q, want /custom/path/foo", targets[0].FinalPath)
	}
}

func TestResolveTargets_MultiAgent(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"opencode", "hermes"},
		InstallMethod: "zip-extract",
	}
	targets, err := ResolveTargets(sv, "foo", "", filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	defer CleanupStaging(targets)
	if len(targets) != 2 {
		t.Fatalf("got %d targets, want 2", len(targets))
	}
	home, _ := os.UserHomeDir()
	wantA := filepath.Join(home, ".config", "opencode", "skills", "foo")
	wantB := filepath.Join(home, ".hermes", "optional-skills", "foo")
	gotA, gotB := targets[0].FinalPath, targets[1].FinalPath
	if !((gotA == wantA && gotB == wantB) || (gotA == wantB && gotB == wantA)) {
		t.Errorf("targets = [%s, %s], want [%s, %s]", gotA, gotB, wantA, wantB)
	}
}

func TestResolveTargets_PipWheelCollapsesToOne(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"opencode", "hermes", "openclaw"},
		InstallMethod: "pip-wheel",
	}
	targets, err := ResolveTargets(sv, "foo", "", filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	defer CleanupStaging(targets)
	if len(targets) != 1 {
		t.Errorf("pip-wheel should collapse to 1 target, got %d", len(targets))
	}
}

func TestResolveTargets_UnknownAgentErrors(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"vscode"}, // not in catalog
		InstallMethod: "zip-extract",
	}
	_, err := ResolveTargets(sv, "foo", "", filepath.Join(t.TempDir(), "staging"))
	if err == nil {
		t.Fatal("expected error for unknown agent")
	}
}

func TestResolveTargets_TargetDirOverrideReplacesAll(t *testing.T) {
	sv := &skills.SkillVersion{
		Agents:        []string{"opencode", "hermes"},
		InstallMethod: "zip-extract",
	}
	// Override collapses to a single <target-dir>/foo path so we don't
	// fan out into the per-agent dirs.
	targets, err := ResolveTargets(sv, "foo", "/override/root", filepath.Join(t.TempDir(), "staging"))
	if err != nil {
		t.Fatalf("ResolveTargets: %v", err)
	}
	defer CleanupStaging(targets)
	if len(targets) != 1 {
		t.Fatalf("got %d targets, want 1 (deduped by override path)", len(targets))
	}
	if targets[0].FinalPath != "/override/root/foo" {
		t.Errorf("FinalPath = %q, want /override/root/foo", targets[0].FinalPath)
	}
}

func TestInstallZipExtract_HappyPath(t *testing.T) {
	zipBytes := makeZipWithFiles(t, map[string]string{
		"SKILL.md":      "---\nname: x\ndescription: y\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n",
		"scripts/a.sh":  "#!/bin/sh\necho hi\n",
	})
	dest := filepath.Join(t.TempDir(), "1.0.0")
	targets := []InstallTarget{
		{Agent: "all", FinalPath: dest, StagingDir: dest + "/.stage"},
	}
	if _, err := Install(InstallRequest{
		SkillName: "x",
		Method:    "zip-extract",
		ZipBytes:  zipBytes,
		Targets:   targets,
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "scripts", "a.sh")); err != nil {
		t.Errorf("scripts/a.sh missing: %v", err)
	}
}

func TestDispatchInstall_UnknownMethod(t *testing.T) {
	_, err := Install(InstallRequest{
		SkillName: "x",
		Method:    "homebrew", // not in catalog
		ZipBytes:  []byte("anything"),
		Targets:   []InstallTarget{{Agent: "all", FinalPath: t.TempDir()}},
	})
	if err == nil || !strings.Contains(err.Error(), "not implemented") {
		t.Errorf("expected not-implemented error, got: %v", err)
	}
}

func TestDispatchInstall_PipWheel_MissingWheelsDir(t *testing.T) {
	zipBytes := makeZipWithFiles(t, map[string]string{
		"SKILL.md": "---\nname: x\ndescription: y\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n",
		"readme.txt": "no wheels here",
	})
	staging := t.TempDir()
	_, err := Install(InstallRequest{
		SkillName: "x",
		Method:    "pip-wheel",
		ZipBytes:  zipBytes,
		Targets:   []InstallTarget{{Agent: "all", FinalPath: t.TempDir(), StagingDir: staging}},
	})
	if err == nil || !strings.Contains(err.Error(), "wheels/") {
		t.Errorf("expected wheels/ error, got: %v", err)
	}
}

func TestDispatchInstall_Tarball_HappyPath(t *testing.T) {
	tgzBytes := makeTarGzWithFiles(t, map[string]string{
		"foo.txt":     "tarball content",
		"sub/bar.txt": "nested content",
	})
	zipBytes := makeZipWithFiles(t, map[string]string{
		"SKILL.md":    "---\nname: x\ndescription: y\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n",
		"payload.tar.gz": string(tgzBytes),
	})
	dest := t.TempDir()
	staging := t.TempDir()
	if _, err := Install(InstallRequest{
		SkillName: "x",
		Method:    "tarball",
		ZipBytes:  zipBytes,
		Targets:   []InstallTarget{{Agent: "all", FinalPath: dest, StagingDir: staging}},
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "foo.txt")); err != nil {
		t.Errorf("foo.txt missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "sub", "bar.txt")); err != nil {
		t.Errorf("sub/bar.txt missing: %v", err)
	}
}

func TestDispatchInstall_Tarball_StripComponents(t *testing.T) {
	tgzBytes := makeTarGzWithFiles(t, map[string]string{
		"top/foo.txt": "stripped",
	})
	zipBytes := makeZipWithFiles(t, map[string]string{
		"SKILL.md":      "---\nname: x\ndescription: y\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n",
		"payload.tar.gz": string(tgzBytes),
	})
	dest := t.TempDir()
	staging := t.TempDir()
	if _, err := Install(InstallRequest{
		SkillName: "x",
		Method:    "tarball",
		ZipBytes:  zipBytes,
		Targets:   []InstallTarget{{Agent: "all", FinalPath: dest, StagingDir: staging}},
		Config: map[string]any{
			"strip_components": 1,
		},
	}); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "foo.txt")); err != nil {
		t.Errorf("foo.txt missing (strip-components=1): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "top", "foo.txt")); err == nil {
		t.Error("top/foo.txt should have been stripped, but exists")
	}
}

func TestDispatchInstall_Tarball_NoTarballErrors(t *testing.T) {
	zipBytes := makeZipWithFiles(t, map[string]string{
		"SKILL.md":  "---\nname: x\ndescription: y\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n",
		"readme.md": "no tarball",
	})
	dest := t.TempDir()
	staging := t.TempDir()
	_, err := Install(InstallRequest{
		SkillName: "x",
		Method:    "tarball",
		ZipBytes:  zipBytes,
		Targets:   []InstallTarget{{Agent: "all", FinalPath: dest, StagingDir: staging}},
	})
	if err == nil || !strings.Contains(err.Error(), ".tar.gz") {
		t.Errorf("expected tarball-not-found error, got: %v", err)
	}
}

func TestFindTarballPayload_MultipleErrors(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.tar.gz"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := findTarballPayload(dir)
	if err == nil {
		t.Fatal("expected error for multiple tarballs")
	}
}

func TestCleanupStaging(t *testing.T) {
	staging := t.TempDir()
	targets := []InstallTarget{{Agent: "all", StagingDir: staging}}
	// Drop a file inside so we can verify removal.
	if err := os.WriteFile(filepath.Join(staging, "sentinel"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	CleanupStaging(targets)
	if _, err := os.Stat(staging); !os.IsNotExist(err) {
		t.Errorf("staging dir should have been removed, stat err = %v", err)
	}
}