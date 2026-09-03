package packagecmd

import (
	"archive/tar"
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAddDirToTarPreservesSymlinks verifies that a symlink in the source
// payload tree is written to the tarball as a TypeSymlink entry pointing at
// the original target, not as a regular file holding the target's bytes.
//
// Regression for the bug where the openclaw launcher ``bin/openclaw`` —
// shipped in payload/openclaw/bin/openclaw as a symlink to
// ``../lib/node_modules/openclaw/openclaw.mjs`` — got emitted into the
// tarball as a 23 KB regular file. ``addFileToTar``'s ``os.Open`` follows
// symlinks; ``addDirToTar`` previously called it for every entry regardless
// of mode. The consumer (``tar -xzf`` in agentpkg install) then extracted
// the launcher as a copy, breaking ``import.meta.url`` resolution at the
// gateway's startup.
func TestAddDirToTarPreservesSymlinks(t *testing.T) {
	root := t.TempDir()

	// Layout mirrors a real agent payload:
	//   payload/bin/openclaw      -> ../lib/node_modules/openclaw/openclaw.mjs
	//   payload/lib/node_modules/openclaw/openclaw.mjs
	binDir := filepath.Join(root, "payload", "bin")
	pkgDir := filepath.Join(root, "payload", "lib", "node_modules", "openclaw")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}
	if err := os.MkdirAll(pkgDir, 0o755); err != nil {
		t.Fatalf("mkdir pkg: %v", err)
	}
	target := "../lib/node_modules/openclaw/openclaw.mjs"
	if err := os.Symlink(target, filepath.Join(binDir, "openclaw")); err != nil {
		t.Fatalf("symlink: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pkgDir, "openclaw.mjs"), []byte("package-launcher-bytes"), 0o755); err != nil {
		t.Fatalf("write launcher: %v", err)
	}

	// Walk from the payload root (which is what ``buildOne`` passes as
	// baseDir for the agent's payload directory).
	tarPath := filepath.Join(root, "out.tar.gz")
	out, err := os.Create(tarPath)
	if err != nil {
		t.Fatalf("create tar: %v", err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	if err := addDirToTar(tw, filepath.Join(root, "payload"), ""); err != nil {
		t.Fatalf("addDirToTar: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tw: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gz: %v", err)
	}

	// Re-read the tarball, classify entries by path.
	entries := readTarEntries(t, tarPath)
	binEntry, ok := entries["bin/openclaw"]
	if !ok {
		t.Fatalf("tarball missing bin/openclaw (got %d entries: %v)", len(entries), keys(entries))
	}
	pkgEntry, ok := entries["lib/node_modules/openclaw/openclaw.mjs"]
	if !ok {
		t.Fatalf("tarball missing lib/node_modules/openclaw/openclaw.mjs (got entries: %v)", keys(entries))
	}

	// if bin/openclaw was written as a TypeReg, addFileToTar followed the
	// symlink and emitted the target's content as a regular file. The fix
	// must produce a TypeSymlink with the original relative target.
	if binEntry.Typeflag != tar.TypeSymlink {
		t.Fatalf("bin/openclaw: got Typeflag=%d (%s), want TypeSymlink — symlink was dereferenced to a regular file (the bug)", binEntry.Typeflag, tarTypeName(binEntry.Typeflag))
	}
	if filepath.Clean(binEntry.Linkname) != filepath.Clean(target) {
		t.Fatalf("bin/openclaw: Linkname=%q, want %q", binEntry.Linkname, target)
	}

	// The package launcher must still be a regular file carrying the
	// original bytes — we only changed symlink handling, not regular files.
	if pkgEntry.Typeflag != tar.TypeReg {
		t.Fatalf("openclaw.mjs: got Typeflag=%d, want TypeReg", pkgEntry.Typeflag)
	}
	if got := string(pkgEntry.Body); got != "package-launcher-bytes" {
		t.Fatalf("openclaw.mjs: body=%q, want %q", got, "package-launcher-bytes")
	}
}

// TestAddDirToTarPreservesRegularFile is the non-regression companion:
// regular files must still be written as TypeReg with their content.
func TestAddDirToTarPreservesRegularFile(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "bin", "openclaw"), []byte("not-a-symlink"), 0o755); err != nil {
		t.Fatal(err)
	}

	tarPath := filepath.Join(root, "out.tar.gz")
	out, err := os.Create(tarPath)
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	gz := gzip.NewWriter(out)
	defer gz.Close()
	tw := tar.NewWriter(gz)
	defer tw.Close()
	if err := addDirToTar(tw, root, ""); err != nil {
		t.Fatalf("addDirToTar: %v", err)
	}
	tw.Close()
	gz.Close()

	entries := readTarEntries(t, tarPath)
	e, ok := entries["bin/openclaw"]
	if !ok {
		t.Fatalf("missing bin/openclaw (entries: %v)", keys(entries))
	}
	if e.Typeflag != tar.TypeReg {
		t.Fatalf("regular file became Typeflag=%d (%s)", e.Typeflag, tarTypeName(e.Typeflag))
	}
	if string(e.Body) != "not-a-symlink" {
		t.Fatalf("body=%q, want %q", string(e.Body), "not-a-symlink")
	}
}

// --- helpers ---

type tarEntry struct {
	Typeflag byte
	Linkname string
	Body     []byte
}

func readTarEntries(t *testing.T, path string) map[string]*tarEntry {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip: %v", err)
	}
	defer gz.Close()
	tr := tar.NewReader(gz)
	out := map[string]*tarEntry{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar.Next: %v", err)
		}
		e := &tarEntry{Typeflag: hdr.Typeflag, Linkname: hdr.Linkname}
		if hdr.Typeflag == tar.TypeReg {
			body, err := io.ReadAll(tr)
			if err != nil {
				t.Fatalf("read body for %s: %v", hdr.Name, err)
			}
			e.Body = body
		}
		out[hdr.Name] = e
	}
	return out
}

func keys(m map[string]*tarEntry) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func tarTypeName(tf byte) string {
	switch tf {
	case tar.TypeReg:
		return "TypeReg"
	case tar.TypeSymlink:
		return "TypeSymlink"
	case tar.TypeDir:
		return "TypeDir"
	case tar.TypeLink:
		return "TypeLink"
	default:
		return strings.TrimSpace(string([]byte{byte('('), tf, byte(')')}))
	}
}