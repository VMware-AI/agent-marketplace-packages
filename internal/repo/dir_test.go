package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// skillsParseIdx is a tiny test helper for parsing a skills index JSON in
// tests. Lives next to parseIdxOrFail (the agent counterpart) for symmetry.
func skillsParseIdx(t *testing.T, raw []byte) (*skills.Index, error) {
	t.Helper()
	return skills.ParseIndexBytes(raw)
}

// parseIdxOrFail is a tiny test helper that bails out (t.Fatal) on any
// parse error so individual cases stay one-line.
func parseIdxOrFail(t *testing.T, raw []byte) *apitypes.Index {
	t.Helper()
	idx, err := apitypes.ParseIndex(raw)
	if err != nil {
		t.Fatalf("parse test index: %v", err)
	}
	return idx
}

// writeFile creates a file with the given bytes and mode 0644 (so tests
// don't accidentally rely on umask). Returns the absolute path.
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, data, 0644); err != nil {
		t.Fatalf("write %s: %v", p, err)
	}
	return p
}

// TestDir_ReadSHA256_ParsesStandardFormat mirrors the format produced by
// `sha256sum`: "<hex>  <filename>\n".
func TestDir_ReadSHA256_ParsesStandardFormat(t *testing.T) {
	dir := t.TempDir()
	const hex = "94d01ff33e3a5bd662340b0d92baf347c12439cae5c9dbd5ad26bdcf604cbc02"
	writeFile(t, dir, "agent.tgz", []byte("ignored"))
	writeFile(t, dir, "agent.tgz.sha256", []byte(hex+"  agent.tgz\n"))

	d := NewDir(dir)
	got, err := d.ReadSHA256("agent.tgz")
	if err != nil {
		t.Fatalf("ReadSHA256: %v", err)
	}
	if got != hex {
		t.Errorf("got %q want %q", got, hex)
	}
}

// TestDir_ReadSHA256_RejectsMalformed covers the malformed-input branch
// so we don't ship a tarball whose sidecar decodes to an empty hash.
func TestDir_ReadSHA256_RejectsMalformed(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "agent.tgz", []byte("ignored"))
	writeFile(t, dir, "agent.tgz.sha256", []byte(""))
	d := NewDir(dir)
	if _, err := d.ReadSHA256("agent.tgz"); err == nil {
		t.Error("expected error on empty sidecar, got nil")
	}
}

// TestDir_ListTarballs_FiltersAndSorts ensures only *.tar.gz files appear
// and that ordering is deterministic (sorted).
func TestDir_ListTarballs_FiltersAndSorts(t *testing.T) {
	dir := t.TempDir()
	// Touch files with varying extensions and one directory.
	for _, n := range []string{"a.tgz", "a.tar.gz", "b.tar.gz", "c.txt", "notes"} {
		writeFile(t, dir, n, []byte("x"))
	}
	// Subdirectory should be ignored.
	if err := os.MkdirAll(filepath.Join(dir, "subdir.tar.gz"), 0755); err != nil {
		t.Fatal(err)
	}

	got, err := NewDir(dir).ListTarballs()
	if err != nil {
		t.Fatalf("ListTarballs: %v", err)
	}
	want := []string{"a.tar.gz", "b.tar.gz"} // ".tgz" suffix excluded
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

// TestDir_FileSHA256_KnownVector locks down the SHA-256 of a fixed input
// so future refactors of the streaming hasher can't silently change
// what gets stamped onto a tarball.
func TestDir_FileSHA256_KnownVector(t *testing.T) {
	dir := t.TempDir()
	p := writeFile(t, dir, "vec", []byte("abc"))
	got, err := FileSHA256(p)
	if err != nil {
		t.Fatalf("FileSHA256: %v", err)
	}
	const want = "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad"
	if got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestDir_Validate_MismatchAndMissing makes sure Validate() surfaces a
// readable error when either the tarball is missing or its hash drifts
// from the index — the two failure modes the marketplace-api startup
// check exists to catch.
func TestDir_Validate_MismatchAndMissing(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "x.tar.gz", []byte("payload"))
	sidecarHex := strings.Repeat("0", 64)
	writeFile(t, dir, "x.tar.gz.sha256", []byte(sidecarHex+"  x.tar.gz\n"))

	d := NewDir(dir)
	good := `{"generated_at":"","schema_version":"1.0","agents":[{"name":"a","display_name":"A","description":"","logo":"","category":"","tags":[],"versions":[{"version":"1","source":"upstream","channel":"stable","tarball":{"filename":"x.tar.gz","size_bytes":7,"sha256":"sha256:` + sidecarHex + `"},"manifest":{}}]}]}`
	if err := d.Validate(parseIdxOrFail(t, []byte(good))); err != nil {
		t.Errorf("good case: %v", err)
	}

	// Mismatch case: index claims a different hash than the sidecar.
	// Replace only the inner hash value (after the second "sha256:") by
	// anchoring on the trailing closing quote + space pattern.
	bad := strings.Replace(good, sidecarHex+`"`, strings.Repeat("1", 64)+`"`, 1)
	if err := d.Validate(parseIdxOrFail(t, []byte(bad))); err == nil {
		t.Error("expected sha256 mismatch error, got nil")
	}

	// Missing case: remove the tarball from disk.
	if err := os.Remove(filepath.Join(dir, "x.tar.gz")); err != nil {
		t.Fatal(err)
	}
	if err := d.Validate(parseIdxOrFail(t, []byte(good))); err == nil {
		t.Error("expected missing-tarball error, got nil")
	}
}

// -----------------------------------------------------------------------
// Skills layout tests (dist/skills/ + dist/skills-index.json).
//
// The skills layout is independent of the agent layout — these tests
// verify the parallel accessors don't accidentally touch dist/ files and
// that the empty-state behaviors (no skills dir, no skills index) are
// "empty result, no error" rather than 404s.
// -----------------------------------------------------------------------

// writeValidSkillZip creates dist/skills/<name>-<source>-<version>.zip +
// a .sha256 sidecar with the matching hash. Used by ValidateSkills tests.
func writeValidSkillZip(t *testing.T, distDir, name, source, version string) (zipName, hexSum string) {
	t.Helper()
	skillsDir := filepath.Join(distDir, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	zipName = name + "-" + source + "-" + version + ".zip"
	zipPath := filepath.Join(skillsDir, zipName)
	payload := []byte("skill zip payload for " + zipName)
	if err := os.WriteFile(zipPath, payload, 0o644); err != nil {
		t.Fatal(err)
	}
	hexSum = FileSHA256Must(t, zipPath)
	if err := os.WriteFile(zipPath+".sha256", []byte(hexSum+"  "+zipName+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return zipName, hexSum
}

// FileSHA256Must is a tiny wrapper used by the skills tests so each
// test doesn't repeat the open/close ceremony.
func FileSHA256Must(t *testing.T, path string) string {
	t.Helper()
	h, err := FileSHA256(path)
	if err != nil {
		t.Fatalf("FileSHA256(%s): %v", path, err)
	}
	return h
}

// TestDir_SkillsPath verifies the parallel directory layout matches the
// plan: dist/skills/ for zips, dist/skills-index.json for the index.
func TestDir_SkillsPath(t *testing.T) {
	d := NewDir("/some/dist")
	if got := d.SkillsPath(); got != "/some/dist/skills" {
		t.Errorf("SkillsPath = %q", got)
	}
	if got := d.SkillsIndexPath(); got != "/some/dist/skills-index.json" {
		t.Errorf("SkillsIndexPath = %q", got)
	}
	if got := d.SkillZipPath("foo-community-1.0.0.zip"); got != "/some/dist/skills/foo-community-1.0.0.zip" {
		t.Errorf("SkillZipPath = %q", got)
	}
	if got := d.SkillSHA256Path("foo-community-1.0.0.zip"); got != "/some/dist/skills/foo-community-1.0.0.zip.sha256" {
		t.Errorf("SkillSHA256Path = %q", got)
	}
}

// TestDir_ListSkillZips_FiltersAndSorts: only *.zip, deterministic order.
func TestDir_ListSkillZips_FiltersAndSorts(t *testing.T) {
	dist := t.TempDir()
	skillsDir := filepath.Join(dist, "skills")
	if err := os.MkdirAll(skillsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"c.zip", "a.zip", "b.zip", "notes.txt", "ignored.tar.gz"} {
		if err := os.WriteFile(filepath.Join(skillsDir, n), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := NewDir(dist).ListSkillZips()
	if err != nil {
		t.Fatalf("ListSkillZips: %v", err)
	}
	want := []string{"a.zip", "b.zip", "c.zip"}
	if len(got) != len(want) {
		t.Fatalf("got %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("[%d] got %q want %q", i, got[i], want[i])
		}
	}
}

// TestDir_ListSkillZips_NoDir: missing dist/skills/ returns empty list,
// not an error — the marketplace-api treats "no skills yet" as a valid state.
func TestDir_ListSkillZips_NoDir(t *testing.T) {
	dist := t.TempDir() // no skills/ subdir
	got, err := NewDir(dist).ListSkillZips()
	if err != nil {
		t.Fatalf("ListSkillZips on missing dir: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty list, got %v", got)
	}
}

// TestDir_LoadSkillsIndex_Missing returns an empty (not nil) Index with
// the schema_version filled in. This is the marketplace-api startup path.
func TestDir_LoadSkillsIndex_Missing(t *testing.T) {
	dist := t.TempDir()
	got, err := NewDir(dist).LoadSkillsIndex()
	if err != nil {
		t.Fatalf("LoadSkillsIndex on missing file: %v", err)
	}
	if got == nil {
		t.Fatal("LoadSkillsIndex returned nil on missing file")
	}
	if got.Schema != "2.0" {
		t.Errorf("Schema = %q, want 2.0", got.Schema)
	}
	if len(got.Skills) != 0 {
		t.Errorf("expected empty Skills, got %d", len(got.Skills))
	}
}

// TestDir_LoadSkillsIndex_Populated round-trips a small index on disk.
func TestDir_LoadSkillsIndex_Populated(t *testing.T) {
	dist := t.TempDir()
	want := `{"generated_at":"2026-09-08","schema_version":"2.0","skills":[{"name":"hello","description":"hi","category":"dev","versions":[{"version":"1.0.0","source":"community","channel":"stable","agents":["all"],"install_method":"zip-extract","zip":{"filename":"hello-community-1.0.0.zip","size_bytes":3,"sha256":"sha256:abc"}}]}]}`
	if err := os.WriteFile(filepath.Join(dist, "skills-index.json"), []byte(want), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := NewDir(dist).LoadSkillsIndex()
	if err != nil {
		t.Fatalf("LoadSkillsIndex: %v", err)
	}
	if len(got.Skills) != 1 || got.Skills[0].Name != "hello" {
		t.Fatalf("unexpected skills: %+v", got.Skills)
	}
}

// TestDir_LoadSkillsIndex_ParseError surfaces the underlying error so the
// reload loop can log it.
func TestDir_LoadSkillsIndex_ParseError(t *testing.T) {
	dist := t.TempDir()
	if err := os.WriteFile(filepath.Join(dist, "skills-index.json"), []byte("{garbage"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewDir(dist).LoadSkillsIndex(); err == nil {
		t.Fatal("expected error on malformed skills-index.json")
	}
}

// TestDir_ValidateSkills_Good: zip + sidecar match index → no error.
func TestDir_ValidateSkills_Good(t *testing.T) {
	dist := t.TempDir()
	zipName, hex := writeValidSkillZip(t, dist, "hello", "community", "1.0.0")
	idxJSON := `{"generated_at":"","schema_version":"1.0","skills":[{"name":"hello","description":"hi","versions":[{"version":"1.0.0","source":"community","channel":"stable","zip":{"filename":"` + zipName + `","size_bytes":1,"sha256":"sha256:` + hex + `"}}]}]}`
	idx, err := skillsParseIdx(t, []byte(idxJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewDir(dist).ValidateSkills(idx); err != nil {
		t.Errorf("good case: %v", err)
	}
}

// TestDir_ValidateSkills_MissingZip: index references a zip that isn't
// on disk → error.
func TestDir_ValidateSkills_MissingZip(t *testing.T) {
	dist := t.TempDir()
	idxJSON := `{"generated_at":"","schema_version":"1.0","skills":[{"name":"hello","description":"hi","versions":[{"version":"1.0.0","source":"community","channel":"stable","zip":{"filename":"ghost-community-1.0.0.zip","size_bytes":0,"sha256":"sha256:` + strings.Repeat("0", 64) + `"}}]}]}`
	idx, err := skillsParseIdx(t, []byte(idxJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewDir(dist).ValidateSkills(idx); err == nil {
		t.Fatal("expected missing-zip error, got nil")
	}
}

// TestDir_ValidateSkills_ShaMismatch: sidecar hex differs from index hash.
func TestDir_ValidateSkills_ShaMismatch(t *testing.T) {
	dist := t.TempDir()
	zipName, hex := writeValidSkillZip(t, dist, "hello", "community", "1.0.0")
	// Build an index that claims a different hash.
	wrong := strings.Repeat("9", 64)
	idxJSON := `{"generated_at":"","schema_version":"1.0","skills":[{"name":"hello","description":"hi","versions":[{"version":"1.0.0","source":"community","channel":"stable","zip":{"filename":"` + zipName + `","size_bytes":1,"sha256":"sha256:` + wrong + `"}}]}]}`
	idx, err := skillsParseIdx(t, []byte(idxJSON))
	if err != nil {
		t.Fatal(err)
	}
	if err := NewDir(dist).ValidateSkills(idx); err == nil {
		t.Errorf("expected sha256 mismatch error, got nil (real hash was %s)", hex)
	}
}
