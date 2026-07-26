package repo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
)

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
	good := `{"generated_at":"","schema_version":"1.0","agents":[{"name":"a","display_name":"A","description":"","icon":"","category":"","tags":[],"versions":[{"version":"1","source":"upstream","channel":"stable","tarball":{"filename":"x.tar.gz","size_bytes":7,"sha256":"sha256:` + sidecarHex + `"},"manifest":{}}]}]}`
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
