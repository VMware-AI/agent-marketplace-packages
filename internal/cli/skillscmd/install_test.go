package skillscmd

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// makeSkillZip creates a zip with SKILL.md (frontmatter + body) and a
// scripts/ subdir containing one executable script. Returns the bytes
// + the hex sha256 of the zip so callers can mock the server's
// /download + /sha256 responses.
func makeSkillZip(t *testing.T, skillMD string) ([]byte, string) {
	t.Helper()
	buf := &bytes.Buffer{}
	zw := zip.NewWriter(buf)
	fh := &zip.FileHeader{Name: "SKILL.md", Method: zip.Deflate}
	fh.SetMode(0o644)
	w, _ := zw.CreateHeader(fh)
	w.Write([]byte(skillMD))
	scripts := &zip.FileHeader{Name: "scripts/run.sh", Method: zip.Deflate}
	scripts.SetMode(0o755)
	w2, _ := zw.CreateHeader(scripts)
	w2.Write([]byte("#!/bin/sh\necho hello\n"))
	zw.Close()
	sum := sha256.Sum256(buf.Bytes())
	return buf.Bytes(), hex.EncodeToString(sum[:])
}

// writeZipFile writes the given bytes to disk and returns the path.
func writeZipFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestInstall_FromLocalZip exercises the --from <path> path which
// skips the registry entirely.
func TestInstall_FromLocalZip(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")

	// Build a local zip.
	zipBytes, sum := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	// Run install. No fake server needed because --from bypasses it.
	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target)
	if err != nil {
		t.Fatalf("install --from: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Installed hello 1.0.0") {
		t.Errorf("missing success message: %s", out)
	}

	// Verify on-disk layout.
	installDir := filepath.Join(target, "hello", "1.0.0")
	if _, err := os.Stat(filepath.Join(installDir, "SKILL.md")); err != nil {
		t.Errorf("SKILL.md missing: %v", err)
	}
	if _, err := os.Stat(filepath.Join(installDir, "scripts", "run.sh")); err != nil {
		t.Errorf("scripts/run.sh missing: %v", err)
	}
	// latest symlink.
	link, err := os.Readlink(filepath.Join(target, "hello", "latest"))
	if err != nil {
		t.Errorf("latest symlink missing: %v", err)
	}
	if link != "1.0.0" {
		t.Errorf("latest symlink points to %q, want 1.0.0", link)
	}
	// state.json.
	stateBytes, err := os.ReadFile(filepath.Join(target, "hello", "state.json"))
	if err != nil {
		t.Fatalf("state.json missing: %v", err)
	}
	var st SkillInstallState
	if err := json.Unmarshal(stateBytes, &st); err != nil {
		t.Fatal(err)
	}
	if st.Name != "hello" || st.CurrentVersion != "1.0.0" {
		t.Errorf("state wrong: %+v", st)
	}
	if st.InstalledVersions["1.0.0"].ZipSHA256 != "sha256:"+sum {
		t.Errorf("sha256 mismatch: %+v", st.InstalledVersions["1.0.0"])
	}
}

// TestInstall_Batch_FromRejected: --from + 2+ names is rejected up-front
// to avoid ambiguous pairing.
func TestInstall_Batch_FromRejected(t *testing.T) {
	dir := t.TempDir()
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	cmd := NewSkillsInstallCmd()
	_, err := runCmd(t, cmd, "alpha", "beta", "--from", zipPath)
	if err == nil {
		t.Fatal("expected --from + multiple names to be rejected")
	}
	if !strings.Contains(err.Error(), "--from") {
		t.Errorf("expected --from error mentioning '--from is single-skill only', got: %v", err)
	}
}

// TestInstall_Batch_AllSucceed: 2 skills served by a fake registry,
// batch install in one invocation. Both must end up on disk and the
// batch must return nil. Mirrors the path taken by
// `agentpkg skills install alpha beta`.
func TestInstall_Batch_AllSucceed(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)

	// Pre-seed the fake server's index with two skills so the CLI can
	// resolve "latest in channel=stable" for each.
	mustSeedFakeSkill(t, fs, "alpha", "0.1.0", "---\nname: alpha\ndescription: First batch skill.\nversion: \"0.1.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	mustSeedFakeSkill(t, fs, "beta", "0.1.0", "---\nname: beta\ndescription: Second batch skill.\nversion: \"0.1.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")

	cfg := fs.configPath(t)
	cred := fs.credentialsPath(t)
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "alpha", "beta")
	if err != nil {
		t.Fatalf("batch install: %v\n%s", err, out)
	}
	// Per-skill success lines emitted by runInstall.
	if !strings.Contains(out, "alpha") {
		t.Errorf("missing alpha in output: %s", out)
	}
	if !strings.Contains(out, "beta") {
		t.Errorf("missing beta in output: %s", out)
	}
	// Both skills should have state.json + payload in the central stash.
	for _, name := range []string{"alpha", "beta"} {
		stPath := filepath.Join(home, ".local", "share", "agentpkg", "skills", name, "state.json")
		if _, err := os.Stat(stPath); err != nil {
			t.Errorf("state.json missing for %s: %v", name, err)
		}
	}
}

// TestInstall_Batch_PartialFailure: one skill exists, one doesn't.
// Batch runs both; the missing one fails (HTTP 404 from registry);
// the existing one succeeds. Final error must be non-nil (so CI
// sees the failure) and must list the failed name.
func TestInstall_Batch_PartialFailure(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	home := t.TempDir()
	t.Setenv("HOME", home)

	mustSeedFakeSkill(t, fs, "alpha", "0.1.0", "---\nname: alpha\ndescription: First batch skill.\nversion: \"0.1.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	// "missing" is NOT seeded — registry will 404 on its list/show.

	cfg := fs.configPath(t)
	cred := fs.credentialsPath(t)
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "alpha", "missing")
	if err == nil {
		t.Fatalf("expected non-nil error from partial-failure batch; out=%s", out)
	}
	// Error must name the failed skill.
	var be *installBatchError
	if !errors.As(err, &be) {
		t.Fatalf("error is not *installBatchError: %T (%v)", err, err)
	}
	if len(be.Statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(be.Statuses))
	}
	failedNames := []string{}
	for _, s := range be.Statuses {
		if !s.OK {
			failedNames = append(failedNames, s.Name)
		}
	}
	if len(failedNames) != 1 || failedNames[0] != "missing" {
		t.Errorf("failed = %v, want [missing]", failedNames)
	}
	// Output must visibly call out the failure for operators tailing logs.
	if !strings.Contains(out, "✗ missing") && !strings.Contains(out, "missing") {
		t.Errorf("output missing failure marker: %s", out)
	}
	if !strings.Contains(out, "1/2") {
		t.Errorf("output missing summary '1/2': %s", out)
	}
	// alpha still got installed.
	stPath := filepath.Join(home, ".local", "share", "agentpkg", "skills", "alpha", "state.json")
	if _, err := os.Stat(stPath); err != nil {
		t.Errorf("alpha state.json missing despite partial failure: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "agentpkg", "skills", "missing")); !os.IsNotExist(err) {
		t.Errorf("missing skill should not have created a dir: %v", err)
	}
}

// TestInstall_Batch_AllFail: 2 skills, both 404. Error must list both
// failed names; alpha + beta state.json must not exist.
func TestInstall_Batch_AllFail(t *testing.T) {
	fs := newFakeServer(t)
	defer fs.Close()
	// Pre-init the index even though we seed nothing — the fake
	// server's {source}/{name} handler dereferences fs.index.Skills
	// without a nil guard.
	fs.index = &skills.Index{Schema: skills.IndexSchemaVersion}
	home := t.TempDir()
	t.Setenv("HOME", home)

	cfg := fs.configPath(t)
	cred := fs.credentialsPath(t)
	SetGlobals(&cfg, &cred)

	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "nope1", "nope2")
	if err == nil {
		t.Fatalf("expected error: %s", out)
	}
	var be *installBatchError
	if !errors.As(err, &be) {
		t.Fatalf("error is not *installBatchError: %T (%v)", err, err)
	}
	if len(be.Statuses) != 2 {
		t.Errorf("expected 2 statuses, got %d", len(be.Statuses))
	}
	for _, s := range be.Statuses {
		if s.OK {
			t.Errorf("status %q unexpectedly OK", s.Name)
		}
	}
	if !strings.Contains(out, "2/2") {
		t.Errorf("output missing summary '2/2': %s", out)
	}
}

// TestInstall_Idempotent: re-running install for the same (source, version)
// is a no-op when the recorded sha256 matches.
func TestInstall_Idempotent(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	// First install.
	cmd := NewSkillsInstallCmd()
	if _, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	// Second install of the same version — should be no-op.
	out, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target)
	if err != nil {
		t.Fatalf("second install: %v\n%s", err, out)
	}
	if !strings.Contains(out, "already installed") {
		t.Errorf("expected idempotent message: %s", out)
	}
}

// TestInstall_ForceOverwrite: re-installing with --force re-extracts.
func TestInstall_ForceOverwrite(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	cmd := NewSkillsInstallCmd()
	if _, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	// Modify the install dir to simulate corruption, then --force.
	installDir := filepath.Join(target, "hello", "1.0.0")
	if err := os.WriteFile(filepath.Join(installDir, "EXTRA"), []byte("garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target, "--force")
	if err != nil {
		t.Fatalf("force install: %v\n%s", err, out)
	}
	if _, err := os.Stat(filepath.Join(installDir, "EXTRA")); !os.IsNotExist(err) {
		t.Errorf("force should have cleaned install dir: %v", err)
	}
}

// TestInstall_DryRun: --dry-run must not touch disk.
func TestInstall_DryRun(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(dry-run)") {
		t.Errorf("expected (dry-run) in output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(target, "hello")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote to disk: %v", err)
	}
}

// TestInstall_DownloadOnly: writes the zip + sidecar but doesn't extract
// or update state.json.
func TestInstall_DownloadOnly_FromLocal(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "hello", "--from", zipPath, "--target-dir", target, "--download-only")
	if err != nil {
		t.Fatalf("download-only: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Downloaded") {
		t.Errorf("missing Downloaded message: %s", out)
	}
	if _, err := os.Stat(filepath.Join(target, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Errorf("download-only wrote install dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "hello", "state.json")); !os.IsNotExist(err) {
		t.Errorf("download-only wrote state.json: %v", err)
	}
}

// TestUninstall_AllVersions: removes the entire per-skill directory.
func TestUninstall_AllVersions(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	// Install first.
	if _, err := runCmd(t, NewSkillsInstallCmd(), "hello", "--from", zipPath, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(target, "hello")); err != nil {
		t.Fatalf("install dir missing: %v", err)
	}
	// Uninstall.
	cmd := NewSkillsUninstallCmd()
	out, err := runCmd(t, cmd, "hello", "--target-dir", target)
	if err != nil {
		t.Fatalf("uninstall: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Uninstalled hello (all versions)") {
		t.Errorf("unexpected output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(target, "hello")); !os.IsNotExist(err) {
		t.Errorf("skill dir still exists: %v", err)
	}
}

// TestUninstall_NotInstalled: should fail with a friendly error.
func TestUninstall_NotInstalled(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	cmd := NewSkillsUninstallCmd()
	_, err := runCmd(t, cmd, "hello", "--target-dir", target)
	if err == nil {
		t.Fatal("expected error for uninstall of non-installed skill")
	}
	if !strings.Contains(err.Error(), "not installed") {
		t.Errorf("expected 'not installed' error: %v", err)
	}
}

// TestUninstall_SingleVersion preserves sibling versions.
func TestUninstall_SingleVersion(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")

	// Install two versions.
	zipV1, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody v1\n")
	zipV1Path := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipV1)
	zipV2, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"2.0.0\"\ncategory: dev\nagents: [all]\n---\nbody v2\n")
	zipV2Path := writeZipFile(t, dir, "hello-community-2.0.0.zip", zipV2)

	if _, err := runCmd(t, NewSkillsInstallCmd(), "hello", "--from", zipV1Path, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	if _, err := runCmd(t, NewSkillsInstallCmd(), "hello", "--from", zipV2Path, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	// Uninstall v1.
	cmd := NewSkillsUninstallCmd()
	out, err := runCmd(t, cmd, "hello", "--version", "1.0.0", "--target-dir", target)
	if err != nil {
		t.Fatalf("uninstall --version 1.0.0: %v\n%s", err, out)
	}
	if !strings.Contains(out, "Uninstalled hello 1.0.0") {
		t.Errorf("unexpected output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(target, "hello", "1.0.0")); !os.IsNotExist(err) {
		t.Errorf("v1 dir still exists: %v", err)
	}
	if _, err := os.Stat(filepath.Join(target, "hello", "2.0.0")); err != nil {
		t.Errorf("v2 should survive: %v", err)
	}
	// Latest should still point at v2 (or have been repointed if v2 was the active).
	link, _ := os.Readlink(filepath.Join(target, "hello", "latest"))
	if link != "2.0.0" {
		t.Errorf("latest symlink = %q, want 2.0.0", link)
	}
}

// TestUninstall_DryRun: --dry-run must not touch disk.
func TestUninstall_DryRun(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	if _, err := runCmd(t, NewSkillsInstallCmd(), "hello", "--from", zipPath, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	out, err := runCmd(t, NewSkillsUninstallCmd(), "hello", "--target-dir", target, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "(dry-run)") {
		t.Errorf("expected (dry-run) in output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(target, "hello")); err != nil {
		t.Errorf("dry-run removed skill dir: %v", err)
	}
}

// TestInstall_PerAgentFanOut: with agents=[opencode, openclaw, hermes]
// and no --target-dir, the install must write to all three per-agent
// default paths (NOT collapse them onto one root). Verifies the bug
// fixed in commit: previously defaultSkillsTargetDir() was always set
// as the targetDir override, which caused ResolveTargets to dedup all
// agents into one path. Also verifies installZipExtract writes to
// every target (was previously Targets[0] only).
func TestInstall_PerAgentFanOut(t *testing.T) {
	dir := t.TempDir()
	// Override $HOME so the per-agent default paths point inside dir.
	t.Setenv("HOME", dir)
	home, _ := os.UserHomeDir()

	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [opencode, openclaw, hermes]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	cmd := NewSkillsInstallCmd()
	out, err := runCmd(t, cmd, "hello", "--from", zipPath)
	if err != nil {
		t.Fatalf("install per-agent: %v\n%s", err, out)
	}

	// All three per-agent paths must have SKILL.md + scripts/run.sh.
	for _, p := range []string{
		filepath.Join(home, ".config", "opencode", "skills", "hello", "1.0.0"),
		filepath.Join(home, ".openclaw", "skills", "hello", "1.0.0"),
		filepath.Join(home, ".hermes", "optional-skills", "hello", "1.0.0"),
	} {
		if _, err := os.Stat(filepath.Join(p, "SKILL.md")); err != nil {
			t.Errorf("missing SKILL.md at %s: %v", p, err)
		}
		if _, err := os.Stat(filepath.Join(p, "scripts", "run.sh")); err != nil {
			t.Errorf("missing scripts/run.sh at %s: %v", p, err)
		}
	}

	// state.json lives in the central stash and records all 3 resolved
	// per-agent paths.
	statePath := filepath.Join(home, ".local", "share", "agentpkg", "skills", "hello", "state.json")
	stateBytes, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("state.json missing at %s: %v", statePath, err)
	}
	var st SkillInstallState
	if err := json.Unmarshal(stateBytes, &st); err != nil {
		t.Fatal(err)
	}
	if len(st.ResolvedPaths) != 3 {
		t.Errorf("ResolvedPaths has %d entries, want 3: %+v", len(st.ResolvedPaths), st.ResolvedPaths)
	}
	for _, agent := range []string{"opencode", "openclaw", "hermes"} {
		if _, ok := st.ResolvedPaths[agent]; !ok {
			t.Errorf("missing ResolvedPaths[%q]: %+v", agent, st.ResolvedPaths)
		}
	}

	// `latest` symlink is intentionally NOT created in per-agent mode
	// (the version lives at the per-agent path, not in the central stash).
	latestLink := filepath.Join(home, ".local", "share", "agentpkg", "skills", "hello", "latest")
	if _, err := os.Lstat(latestLink); !os.IsNotExist(err) {
		t.Errorf("unexpected latest symlink in per-agent mode: %v", err)
	}

	// Uninstall must remove all three per-agent dirs (no --target-dir).
	cmdU := NewSkillsUninstallCmd()
	if _, err := runCmd(t, cmdU, "hello"); err != nil {
		t.Fatalf("uninstall per-agent: %v", err)
	}
	for _, p := range []string{
		filepath.Join(home, ".config", "opencode", "skills", "hello"),
		filepath.Join(home, ".openclaw", "skills", "hello"),
		filepath.Join(home, ".hermes", "optional-skills", "hello"),
	} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("per-agent dir still present after uninstall: %s", p)
		}
	}
	if _, err := os.Stat(filepath.Join(home, ".local", "share", "agentpkg", "skills", "hello")); !os.IsNotExist(err) {
		t.Errorf("central stash still present after uninstall")
	}
}

// TestListInstalled_OK: lists installed skills in deterministic order.
func TestListInstalled_OK(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)

	if _, err := runCmd(t, NewSkillsInstallCmd(), "hello", "--from", zipPath, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	cmd := NewSkillsListInstalledCmd()
	out, err := runCmd(t, cmd, "--target-dir", target)
	if err != nil {
		t.Fatalf("list-installed: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("expected 'hello' in output: %s", out)
	}
	if !strings.Contains(out, "1.0.0") {
		t.Errorf("expected '1.0.0' in output: %s", out)
	}
}

// TestListInstalled_Empty: an empty target dir reports "(no skills installed)".
func TestListInstalled_Empty(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	cmd := NewSkillsListInstalledCmd()
	out, err := runCmd(t, cmd, "--target-dir", target)
	if err != nil {
		t.Fatalf("list-installed empty: %v\n%s", err, out)
	}
	if !strings.Contains(out, "no skills installed") {
		t.Errorf("expected 'no skills installed' message: %s", out)
	}
}

// TestListInstalled_JSON: --json emits an array of installedRow objects.
func TestListInstalled_JSON(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "skills")
	zipBytes, _ := makeSkillZip(t, "---\nname: hello\ndescription: Says hello to the user in a friendly manner.\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n")
	zipPath := writeZipFile(t, dir, "hello-community-1.0.0.zip", zipBytes)
	if _, err := runCmd(t, NewSkillsInstallCmd(), "hello", "--from", zipPath, "--target-dir", target); err != nil {
		t.Fatal(err)
	}
	cmd := NewSkillsListInstalledCmd()
	out, err := runCmd(t, cmd, "--target-dir", target, "--json")
	if err != nil {
		t.Fatalf("list-installed --json: %v\n%s", err, out)
	}
	var rows []installedRow
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatalf("parse json: %v\n%s", err, out)
	}
	if len(rows) != 1 || rows[0].Name != "hello" {
		t.Errorf("unexpected rows: %+v", rows)
	}
}

// TestStateFileRoundTrip: save + load a state and verify equality.
func TestStateFileRoundTrip(t *testing.T) {
	dir := t.TempDir()
	st := &SkillInstallState{
		Name:           "hello",
		CurrentVersion: "1.0.0",
		Source:         "community",
		Channel:        "stable",
		TargetDir:      dir,
		InstalledVersions: map[string]InstalledVersion{
			"1.0.0": {Source: "community", Channel: "stable", ZipSHA256: "sha256:abcd"},
		},
	}
	if err := saveSkillState(dir, st); err != nil {
		t.Fatal(err)
	}
	got, err := loadSkillState(dir, "hello")
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || got.CurrentVersion != "1.0.0" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
}

// TestLoadSkillState_Missing: loadSkillState returns (nil, nil) when
// state.json doesn't exist (clean "not installed" signal).
func TestLoadSkillState_Missing(t *testing.T) {
	dir := t.TempDir()
	st, err := loadSkillState(dir, "no-such-skill")
	if err != nil {
		t.Errorf("expected nil error for missing state, got: %v", err)
	}
	if st != nil {
		t.Errorf("expected nil state, got: %+v", st)
	}
}

// TestSemverCompare is a smoke test for the semver comparison helper,
// which is the foundation for "latest" symlink ordering.
func TestSemverCompare(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"1.0.0", "2.0.0", -1},
		{"2.0.0", "1.0.0", 1},
		{"1.0.0", "1.0.0", 0},
		{"1.10.0", "1.9.0", 1},
		{"1.0.0", "1.0.0-beta.1", 1},
	}
	for _, c := range cases {
		got := skills.SemverCompare(c.a, c.b)
		gotSign := 0
		if got < 0 {
			gotSign = -1
		} else if got > 0 {
			gotSign = 1
		}
		if gotSign != c.want {
			t.Errorf("SemverCompare(%q, %q) = sign(%d), want sign(%d)", c.a, c.b, got, c.want)
		}
	}
}