package skillscmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// runCmd runs a cobra command with the given args and returns the
// combined stdout/stderr output plus any error. Cobra's SetOut/SetErr
// accept io.Writer, so we pass *bytes.Buffer directly (it implements
// io.Writer).
func runCmd(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()
	var out, errOut bytes.Buffer
	cmd.SetOut(&out)
	cmd.SetErr(&errOut)
	cmd.SetArgs(args)
	execErr := cmd.Execute()
	return out.String() + errOut.String(), execErr
}

// validSkillMD is a minimal-but-complete SKILL.md that passes Validate.
const validSkillMD = "---\n" +
	"name: hello\n" +
	"description: Says hello to the user in a friendly manner.\n" +
	"version: \"1.0.0\"\n" +
	"author: Tester\n" +
	"category: dev\n" +
	"agents: [all]\n" +
	"tags: [greeting]\n" +
	"metadata:\n" +
	"  entry_point: scripts/run.sh\n" +
	"---\n" +
	"\n" +
	"# Hello\n" +
	"\n" +
	"Long body.\n"

// writeValidSkillDir creates a complete skill staging directory at
// <TempDir>/hello (matching the skill name in validSkillMD) and returns
// the path. The directory name MUST match the skill name because
// `agentpkg skills build` enforces the identity match (a copy-paste or
// typo in the directory name is otherwise silent).
func writeValidSkillDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "hello")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte(validSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	scriptsDir := filepath.Join(dir, "scripts")
	if err := os.MkdirAll(scriptsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(scriptsDir, "run.sh"), []byte("#!/bin/sh\necho hello\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

// -----------------------------------------------------------------------
// init
// -----------------------------------------------------------------------

func TestSkillsInit_OK(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "skills", "hello")
	cmd := NewSkillsInitCmd()
	out, err := runCmd(t, cmd, "hello", "--dir", target, "--author", "Alice")
	if err != nil {
		t.Fatalf("init: %v\n%s", err, out)
	}
	for _, want := range []string{
		filepath.Join(target, "SKILL.md"),
		filepath.Join(target, "scripts", ".gitkeep"),
		filepath.Join(target, "references", ".gitkeep"),
		filepath.Join(target, "assets", ".gitkeep"),
		filepath.Join(target, "workflows", ".gitkeep"),
	} {
		if _, err := os.Stat(want); err != nil {
			t.Errorf("expected %s to exist: %v", want, err)
		}
	}
	// SKILL.md should have the name + author + a real date substituted.
	data, _ := os.ReadFile(filepath.Join(target, "SKILL.md"))
	body := string(data)
	if !strings.Contains(body, "name: hello") {
		t.Errorf("SKILL.md missing 'name: hello': %s", body)
	}
	if !strings.Contains(body, "author: Alice") {
		t.Errorf("SKILL.md missing 'author: Alice': %s", body)
	}
	if !strings.Contains(body, "created_at: 202") {
		t.Errorf("SKILL.md missing date 'created_at: 202...': %s", body)
	}
}

func TestSkillsInit_RefusesExisting(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "skills", "hello")
	cmd := NewSkillsInitCmd()
	out, err := runCmd(t, cmd, "hello", "--dir", target)
	if err != nil {
		t.Fatalf("first init: %v\n%s", err, out)
	}
	cmd = NewSkillsInitCmd()
	out, err = runCmd(t, cmd, "hello", "--dir", target)
	if err == nil {
		t.Error("expected error on second init without --force")
	}
	if !strings.Contains(out, "already exists") {
		t.Errorf("error message missing 'already exists': %s", out)
	}
}

func TestSkillsInit_Force(t *testing.T) {
	tmp := t.TempDir()
	target := filepath.Join(tmp, "skills", "hello")
	cmd := NewSkillsInitCmd()
	if _, err := runCmd(t, cmd, "hello", "--dir", target); err != nil {
		t.Fatal(err)
	}
	cmd = NewSkillsInitCmd()
	if _, err := runCmd(t, cmd, "hello", "--dir", target, "--force"); err != nil {
		t.Errorf("--force should allow re-init: %v", err)
	}
}

// -----------------------------------------------------------------------
// verify
// -----------------------------------------------------------------------

func TestSkillsVerify_Directory(t *testing.T) {
	dir := writeValidSkillDir(t)
	cmd := NewSkillsVerifyCmd()
	out, err := runCmd(t, cmd, dir)
	if err != nil {
		t.Fatalf("verify: %v\n%s", err, out)
	}
	for _, want := range []string{"hello", "1.0.0", "Tester", "scripts/run.sh"} {
		if !strings.Contains(out, want) {
			t.Errorf("verify output missing %q: %s", want, out)
		}
	}
}

func TestSkillsVerify_File(t *testing.T) {
	dir := writeValidSkillDir(t)
	cmd := NewSkillsVerifyCmd()
	out, err := runCmd(t, cmd, filepath.Join(dir, "SKILL.md"))
	if err != nil {
		t.Fatalf("verify file: %v\n%s", err, out)
	}
	if !strings.Contains(out, "hello") {
		t.Errorf("missing skill name: %s", out)
	}
}

func TestSkillsVerify_InvalidManifest(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"),
		[]byte("---\nname: X\ndescription: too short\nversion: \"1.0.0\"\ncategory: dev\nagents: [all]\n---\nbody\n"),
		0o644); err != nil {
		t.Fatal(err)
	}
	cmd := NewSkillsVerifyCmd()
	_, err := runCmd(t, cmd, dir)
	if err == nil {
		t.Error("expected error for invalid SKILL.md")
	}
}

// -----------------------------------------------------------------------
// build
// -----------------------------------------------------------------------

func TestSkillsBuild_OK(t *testing.T) {
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	out, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--source", "community", "--out", outDir)
	if err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	zipPath := filepath.Join(outDir, "skills", "hello-community-1.0.0.zip")
	sidePath := zipPath + ".sha256"
	if _, err := os.Stat(zipPath); err != nil {
		t.Errorf("zip not written: %v", err)
	}
	if _, err := os.Stat(sidePath); err != nil {
		t.Errorf("sidecar not written: %v", err)
	}
	data, _ := os.ReadFile(sidePath)
	if !strings.Contains(string(data), "hello-community-1.0.0.zip\n") {
		t.Errorf("sidecar format unexpected: %q", string(data))
	}
	idxPath := filepath.Join(outDir, "skills-index.json")
	idxData, _ := os.ReadFile(idxPath)
	var idx skills.Index
	if err := json.Unmarshal(idxData, &idx); err != nil {
		t.Fatalf("parse index: %v\n%s", err, idxData)
	}
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "hello" {
		t.Errorf("index wrong: %+v", idx.Skills)
	}
	if len(idx.Skills[0].Versions) != 1 || idx.Skills[0].Versions[0].Version != "1.0.0" {
		t.Errorf("index versions wrong: %+v", idx.Skills[0].Versions)
	}
}

func TestSkillsBuild_DryRun(t *testing.T) {
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	out, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--source", "community", "--out", outDir, "--dry-run")
	if err != nil {
		t.Fatalf("dry-run build: %v", err)
	}
	if !strings.Contains(out, "(dry-run)") {
		t.Errorf("expected dry-run output: %s", out)
	}
	if _, err := os.Stat(filepath.Join(outDir, "skills")); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote skills/ directory: err=%v", err)
	}
}

func TestSkillsBuild_NameMismatch(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "different"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "different", "SKILL.md"),
		[]byte(validSkillMD), 0o644); err != nil {
		t.Fatal(err)
	}
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	out, err := runCmd(t, cmd, filepath.Join(dir, "different"), "--version", "1.0.0", "--out", outDir)
	if err == nil {
		t.Fatal("expected error for name mismatch")
	}
	if !strings.Contains(out, "does not match") {
		t.Errorf("expected mismatch error: %s", out)
	}
}

func TestSkillsBuild_InvalidVersion(t *testing.T) {
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	_, err := runCmd(t, cmd, dir, "--version", "1.0", "--out", outDir)
	if err == nil {
		t.Error("expected error for non-strict-semver version")
	}
}

func TestSkillsBuild_InvalidSource(t *testing.T) {
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	_, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--source", "evil", "--out", outDir)
	if err == nil {
		t.Error("expected error for invalid --source")
	}
}

func TestSkillsBuild_ReindexAfterBuild(t *testing.T) {
	// Build two different versions of the same skill — reindex should
	// produce a single Skill entry with two versions.
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()

	cmd := NewSkillsBuildCmd()
	if _, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--source", "community", "--out", outDir); err != nil {
		t.Fatal(err)
	}
	cmd = NewSkillsBuildCmd()
	if _, err := runCmd(t, cmd, dir, "--version", "1.1.0", "--source", "community", "--out", outDir); err != nil {
		t.Fatal(err)
	}

	idxData, _ := os.ReadFile(filepath.Join(outDir, "skills-index.json"))
	var idx skills.Index
	json.Unmarshal(idxData, &idx)
	if len(idx.Skills) != 1 || idx.Skills[0].Name != "hello" {
		t.Fatalf("expected one skill 'hello', got %+v", idx.Skills)
	}
	if len(idx.Skills[0].Versions) != 2 {
		t.Errorf("expected 2 versions, got %d", len(idx.Skills[0].Versions))
	}
	if idx.Skills[0].Versions[0].Version != "1.1.0" {
		t.Errorf("first version = %q, want 1.1.0 (semver-sorted)", idx.Skills[0].Versions[0].Version)
	}
}

// -----------------------------------------------------------------------
// sign (dry-run only — actual gpg would need a key)
// -----------------------------------------------------------------------

func TestSkillsSign_DryRun(t *testing.T) {
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	if _, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--out", outDir); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(outDir, "skills", "hello-community-1.0.0.zip")

	cmd = NewSkillsSignCmd()
	out, err := runCmd(t, cmd, zip, "--dry-run")
	if err != nil {
		t.Fatalf("sign dry-run: %v\n%s", err, out)
	}
	if !strings.Contains(out, "gpg") {
		t.Errorf("dry-run output missing 'gpg': %s", out)
	}
	if _, err := os.Stat(zip + ".asc"); !os.IsNotExist(err) {
		t.Errorf("dry-run wrote .asc: err=%v", err)
	}
}

func TestSkillsSign_MissingZip(t *testing.T) {
	cmd := NewSkillsSignCmd()
	_, err := runCmd(t, cmd, "/nonexistent/whatever.zip")
	if err == nil {
		t.Error("expected error for missing zip")
	}
}

func TestSkillsSign_GpgMissing(t *testing.T) {
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	if _, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--out", outDir); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(outDir, "skills", "hello-community-1.0.0.zip")

	cmd = NewSkillsSignCmd()
	_, err := runCmd(t, cmd, zip, "--gpg", "/nonexistent/gpg-binary")
	if err == nil {
		t.Error("expected error when gpg binary is missing")
	}
}

// TestSkillsSign_RealGPG is opt-in: only runs if GPG is available AND
// the user has set AGENTPKG_TEST_GPG=1 in the environment. This avoids
// polluting test runs on machines without gpg installed.
func TestSkillsSign_RealGPG(t *testing.T) {
	if os.Getenv("AGENTPKG_TEST_GPG") != "1" {
		t.Skip("AGENTPKG_TEST_GPG not set")
	}
	if _, err := exec.LookPath("/usr/bin/gpg"); err != nil {
		t.Skip("gpg not installed")
	}
	dir := writeValidSkillDir(t)
	outDir := t.TempDir()
	cmd := NewSkillsBuildCmd()
	if _, err := runCmd(t, cmd, dir, "--version", "1.0.0", "--out", outDir); err != nil {
		t.Fatal(err)
	}
	zip := filepath.Join(outDir, "skills", "hello-community-1.0.0.zip")

	cmd = NewSkillsSignCmd()
	if _, err := runCmd(t, cmd, zip); err != nil {
		t.Fatalf("real gpg sign: %v", err)
	}
	if _, err := os.Stat(zip + ".asc"); err != nil {
		t.Errorf(".asc not produced: %v", err)
	}
}

// -----------------------------------------------------------------------
// helpers (parseSkillZipName, sha256File, humanSize)
// -----------------------------------------------------------------------

func TestParseSkillZipName(t *testing.T) {
	// ParseZipFilename accepts 1-, 2-, or 3-segment stems so that
	// community zips named "<name>.zip" / "<name>-<version>.zip" don't
	// need a synthetic source segment. Source is resolved from the
	// filename only when the 3-segment form has a valid source in the
	// middle slot (community / internal); otherwise the 3-segment form
	// is interpreted as "<name-with-dash>-<version>" — the middle word
	// is part of the name, not a source.
	cases := []struct {
		name, file, n, s, v string
		wantErr             bool
	}{
		{"semver_3seg", "hello-community-1.0.0.zip", "hello", "community", "1.0.0", false},
		// "upstream" is not a valid source (only community/internal are),
		// so 3-segment form falls through to 2-segment interpretation.
		{"calver_3seg_invalid_middle", "openclaw-upstream-2026.7.1.zip", "openclaw-upstream", "", "2026.7.1", false},
		{"internal_3seg", "skills-internal-0.1.0.zip", "skills", "internal", "0.1.0", false},
		// 3-segment with invalid middle segment → fall back to 2-segment
		// interpretation. This is what the ppt-maker-1.0.3.zip community
		// uploads hit before this fix.
		{"dotted_version_with_dash_in_name", "ppt-maker-1.0.3.zip", "ppt-maker", "", "1.0.3", false},
		{"underscore_name", "my_skill-internal-0.1.0.zip", "my_skill", "internal", "0.1.0", false},
		{"name_only", "hello.zip", "hello", "", "", false},
		{"name_version", "hello-1.0.0.zip", "hello", "", "1.0.0", false},
		{"not_zip", "hello-community-1.0.0.tar.gz", "", "", "", true},
		{"too_many_dashes", "a-b-c-d.zip", "", "", "", true},
		{"empty_name", "-community-1.0.0.zip", "", "", "", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n, s, v, err := parseSkillZipName(tc.file)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error for %q", tc.file)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if n != tc.n || s != tc.s || v != tc.v {
				t.Errorf("got (%q, %q, %q), want (%q, %q, %q)", n, s, v, tc.n, tc.s, tc.v)
			}
		})
	}
}

func TestSha256File_KnownVector(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	if err := os.WriteFile(p, []byte("abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := sha256File(p)
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256([]byte("abc\n"))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("sha256 = %s, want %s", got, want)
	}
}

func TestSha256File_Missing(t *testing.T) {
	if _, err := sha256File("/nonexistent/path"); err == nil {
		t.Error("expected error for missing file")
	}
}

func TestHumanSize(t *testing.T) {
	cases := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
	}
	for _, tc := range cases {
		if got := humanSize(tc.in); got != tc.want {
			t.Errorf("humanSize(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFileSize(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "f")
	os.WriteFile(p, []byte("hello"), 0o644)
	if got := fileSize(p); got != 5 {
		t.Errorf("fileSize = %d, want 5", got)
	}
	if got := fileSize("/nonexistent"); got != 0 {
		t.Errorf("fileSize(missing) = %d, want 0", got)
	}
}

func TestTitleCase(t *testing.T) {
	cases := []struct{ in, want string }{
		{"hello", "Hello"},
		{"web-search", "Web-search"},
		{"", ""},
		{"X", "X"},
		{"ABC", "ABC"}, // already uppercase: function capitalizes only 'a-z'
	}
	for _, tc := range cases {
		if got := titleCase(tc.in); got != tc.want {
			t.Errorf("titleCase(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestWriteFileAtomic(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "out.txt")
	if err := writeFileAtomic(p, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(p)
	if string(got) != "hello" {
		t.Errorf("content = %q", string(got))
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Errorf("leftover tmp file: %s", e.Name())
		}
	}
}

func TestZipNamesInDist(t *testing.T) {
	dir := t.TempDir()
	for _, n := range []string{"a.zip", "b.zip", "c.txt", ".tmp-build-x"} {
		os.WriteFile(filepath.Join(dir, n), []byte("x"), 0o644)
	}
	os.MkdirAll(filepath.Join(dir, "subdir"), 0o755)

	got, err := zipNamesInDist(dir)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a.zip", "b.zip"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q, want %q", i, got[i], want[i])
		}
	}
}

func TestZipNamesInDist_Missing(t *testing.T) {
	got, err := zipNamesInDist("/nonexistent/path")
	if err != nil {
		t.Fatalf("missing dir should not error: %v", err)
	}
	if got != nil {
		t.Errorf("expected nil for missing dir, got %v", got)
	}
}

// -----------------------------------------------------------------------
// Stubs (not yet implemented) — just confirm they return errors.
// -----------------------------------------------------------------------

// TestStubs_ReturnNotImplemented is now empty — all skills subcommands
// have real implementations. Kept as a no-op marker so future stubs
// (e.g. if Phase 9 adds a `skills promote` that needs the regression
// check) have a one-line home.
