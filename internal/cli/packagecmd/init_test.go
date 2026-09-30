package packagecmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
)

// TestScaffoldAgent_BuildReady is the round-trip regression for F024:
//
//   `agentpkg package init <name>` previously produced a directory with
//   literal `<display_name>` / `<category>` / etc. placeholders in
//   meta.yaml, `sha256:TBD` in every manifest.checksums entry, and a
//   `.gitkeep` at the payload binary path. Operators had to hand-edit
//   ~30 lines before `package build` would succeed. The fix substitutes
//   real defaults at scaffold time and embeds the actual sha256s of the
//   install.sh / uninstall.sh / payload/bin/<name> files. This test
//   calls `scaffoldAgent` and then `verifyAgent` — both must succeed
//   without any hand-editing in between.
func TestScaffoldAgent_BuildReady(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "my-test-agent")
	if err := scaffoldAgent("my-test-agent", agentDir); err != nil {
		t.Fatalf("scaffoldAgent: %v", err)
	}

	// meta.yaml must validate AND contain no `<...>` placeholders —
	// the literal angle-bracket markers are the bug-class that F024
	// classified. We grep for them defensively; if a future template
	// re-introduces an unrendered placeholder it'll trip here.
	metaBody, err := os.ReadFile(filepath.Join(agentDir, "meta.yaml"))
	if err != nil {
		t.Fatalf("read meta.yaml: %v", err)
	}
	if strings.Contains(string(metaBody), "<display_name>") ||
		strings.Contains(string(metaBody), "<category>") ||
		strings.Contains(string(metaBody), "<runtime_type>") ||
		strings.Contains(string(metaBody), "<logo>") ||
		strings.Contains(string(metaBody), "<description>") {
		t.Errorf("meta.yaml still has `<...>` placeholders:\n%s", metaBody)
	}

	// The rendered meta.yaml must round-trip through manifest.LoadMeta
	// — that's the same validation the consumer's install pipeline
	// runs on the tarball. A regression where renderMeta emits a
	// category not in validCategories (or a description shorter than
	// 10 chars) would slip past the no-placeholder check but fail
	// here.
	metaPath := filepath.Join(agentDir, "meta.yaml")
	m, err := manifest.LoadMeta(metaPath)
	if err != nil {
		t.Fatalf("manifest.LoadMeta failed on scaffolded meta.yaml: %v\n---\n%s", err, metaBody)
	}
	m.Defaults("my-test-agent")

	// manifest.json's `checksums` must NOT contain "sha256:TBD" —
	// the old scaffold emitted TBD for all three entries, which made
	// `package build` (and downstream `agentpkg install`) reject the
	// tarball. The fix computes real sha256s at scaffold time.
	mfBody, err := os.ReadFile(filepath.Join(agentDir, "upstream", "0.1.0", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest.json: %v", err)
	}
	if strings.Contains(string(mfBody), "sha256:TBD") {
		t.Errorf("manifest.json still has sha256:TBD entries:\n%s", mfBody)
	}

	// payload/bin/<name> must exist and be a regular file, NOT a
	// .gitkeep stub. install.sh's post-install smoke check runs
	// `<bin> --version`; an empty .gitkeep fails that check (exit
	// non-zero on missing arg), which makes the scaffolded install
	// chain abort. The fix writes a small bash script that handles
	// `--version` and exits 0.
	binaryPath := filepath.Join(agentDir, "upstream", "0.1.0", "payload", "bin", "my-test-agent")
	fi, err := os.Stat(binaryPath)
	if err != nil {
		t.Fatalf("payload bin not written: %v", err)
	}
	if !fi.Mode().IsRegular() {
		t.Errorf("payload bin is not regular file: mode=%v", fi.Mode())
	}
	if fi.Mode().Perm()&0o111 == 0 {
		t.Errorf("payload bin is not executable: mode=%v", fi.Mode())
	}

	// install.sh + uninstall.sh must also be executable — the
	// previous scaffold wrote them at 0644, which silently passed
	// build but failed install at the kernel's EACCES on execve.
	for _, script := range []string{"install.sh", "uninstall.sh"} {
		p := filepath.Join(agentDir, "upstream", "0.1.0", script)
		fi, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %s: %v", script, err)
		}
		if fi.Mode().Perm()&0o111 == 0 {
			t.Errorf("%s is not executable: mode=%v", script, fi.Mode())
		}
	}

	// Now the actual end-to-end check: `package verify` must succeed
	// without hand-editing. verifyAgent re-reads each manifest.checksums
	// file, recomputes its sha256, and compares. If the manifest's
	// recorded sha256 doesn't match the actual file bytes (e.g. the
	// scaffold wrote a placeholder hash), this fails.
	if err := verifyAgent(agentDir, true); err != nil {
		t.Errorf("verifyAgent failed on freshly-scaffolded directory: %v", err)
	}

	// The scaffolded binary's --version handler must exit 0 —
	// install.sh runs it post-install and treats non-zero exit as a
	// failed install. Run it directly to confirm.
	cmd := exec.Command(binaryPath, "--version")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Errorf("payload bin --version failed: %v (output: %s)", err, out)
	}
	if !strings.Contains(string(out), "my-test-agent") {
		t.Errorf("payload bin --version output missing agent name: %q", out)
	}
}

// TestRenderMeta_ValidatesForVariousNames exercises renderMeta across a
// few realistic agent names — short single-letter, multi-segment with
// dashes — to catch a regression where the templated description or
// category would fail the manifest validator (e.g. an extra-short name
// producing a sub-10-char description, or a name containing characters
// that need escaping inside the YAML double-quoted string).
func TestRenderMeta_ValidatesForVariousNames(t *testing.T) {
	for _, name := range []string{"a", "openclaw", "x-with-dashes", "agent123"} {
		body := renderMeta(name)
		tmp := filepath.Join(t.TempDir(), "meta.yaml")
		if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
			t.Fatalf("write meta for %q: %v", name, err)
		}
		if _, err := manifest.LoadMeta(tmp); err != nil {
			t.Errorf("renderMeta(%q) failed LoadMeta: %v\n---\n%s", name, err, body)
		}
	}
}

// TestScaffoldAgent_RefusesExisting confirms the overwrite-guard
// still fires after the F024 refactor. The previous scaffoldAgent
// had a `os.Stat(root)` check that returned "directory already exists";
// we keep that behavior so an author who reruns `init` doesn't
// silently clobber an in-progress package.
func TestScaffoldAgent_RefusesExisting(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "my-test-agent")
	if err := scaffoldAgent("my-test-agent", agentDir); err != nil {
		t.Fatalf("first scaffold: %v", err)
	}
	if err := scaffoldAgent("my-test-agent", agentDir); err == nil {
		t.Fatal("second scaffold should fail (existing directory), got nil")
	}
}