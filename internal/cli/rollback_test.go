package cli

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestReadStatePrevious_OK verifies the happy-path state.json read:
// state.json contains a usable .previous block, both currentVersion and
// (targetVersion, targetSource) come out correctly.
func TestReadStatePrevious_OK(t *testing.T) {
	dir := t.TempDir()
	state := map[string]any{
		"agent":   "opencode",
		"source":  "upstream",
		"version": "1.18.9",
		"previous": map[string]any{
			"version": "1.18.5",
			"source":  "upstream",
		},
	}
	data, _ := json.MarshalIndent(state, "", "  ")
	statePath := filepath.Join(dir, "state", "opencode.state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(statePath, data, 0644); err != nil {
		t.Fatalf("write: %v", err)
	}

	cur, targetV, targetSrc, err := readStatePrevious("opencode", dir)
	if err != nil {
		t.Fatalf("readStatePrevious: %v", err)
	}
	if cur != "1.18.9" {
		t.Errorf("currentVersion = %q, want 1.18.9", cur)
	}
	if targetV != "1.18.5" {
		t.Errorf("targetVersion = %q, want 1.18.5", targetV)
	}
	if targetSrc != "upstream" {
		t.Errorf("targetSource = %q, want upstream", targetSrc)
	}
}

// TestReadStatePrevious_Missing covers the case that maps to exit 74:
// state.json either doesn't exist, has no .previous block, or has an
// empty .previous.version. In all three the helper must return a
// *rollbackError with code ExitRollbackNoTarget (74).
func TestReadStatePrevious_Missing(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		name    string
		write   string // file content; "" means skip writing
		missing bool   // skip writing state.json at all
	}{
		{"file missing", "", true},
		{"no previous", `{"agent":"opencode","version":"1.18.9","source":"upstream"}`, false},
		{"empty previous", `{"previous":{}}`, false},
		{"previous not object", `{"previous":"oops"}`, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			d := filepath.Join(dir, tc.name)
			if err := os.MkdirAll(filepath.Join(d, "state"), 0755); err != nil {
				t.Fatalf("mkdir: %v", err)
			}
			if !tc.missing {
				if err := os.WriteFile(filepath.Join(d, "state", "opencode.state.json"), []byte(tc.write), 0644); err != nil {
					t.Fatalf("write: %v", err)
				}
			}
			_, _, _, err := readStatePrevious("opencode", d)
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			var rb *rollbackError
			if !errors.As(err, &rb) {
				t.Fatalf("err type %T, want *rollbackError", err)
			}
			if rb.code != ExitRollbackNoTarget {
				t.Errorf("code = %d, want %d (ExitRollbackNoTarget)", rb.code, ExitRollbackNoTarget)
			}
		})
	}
}

// TestHasMigrateToScript_Present covers the discovery helper:
// hasMigrateToScript must return true exactly when the **target tarball**
// carries a migrate/to-<from>.sh entry, where the layout is
//   <targetVersion>/migrate/to-<fromVersion>.sh
// (the canonical tools/pack.sh layout). False negatives (no script at
// all) and false positives (similar basename under a different dir, e.g.
// "to-1.18.9.sh" sitting in payload/) must both be rejected.
//
// Real-world scenario under test: rolling back 1.18.9 -> 1.18.5.
// targetVersion=1.18.5, fromVersion=1.18.9, the script sits at
//   1.18.5/migrate/to-1.18.9.sh
// inside the 1.18.5 tarball.
func TestHasMigrateToScript_Present(t *testing.T) {
	dir := t.TempDir()
	const targetV, fromV = "1.18.5", "1.18.9"

	// Layout A: nested under <targetVersion>/migrate/ (canonical).
	pathA := filepath.Join(dir, "nested.tgz")
	if err := os.WriteFile(pathA, makeTarGz(t, map[string]string{
		"1.18.5/migrate/to-1.18.9.sh": "#!/bin/sh\nexit 0\n",
		"1.18.5/manifest.json":        `{}`,
	}), 0644); err != nil {
		t.Fatal(err)
	}
	if !hasMigrateToScript(pathA, targetV, fromV) {
		t.Error("nested layout: hasMigrateToScript = false, want true")
	}

	// Layout B: at root migrate/ (fallback for tarballs that don't
	// nest under <version>/).
	pathB := filepath.Join(dir, "root.tgz")
	if err := os.WriteFile(pathB, makeTarGz(t, map[string]string{
		"migrate/to-1.18.9.sh": "#!/bin/sh\nexit 0\n",
		"manifest.json":        `{}`,
	}), 0644); err != nil {
		t.Fatal(err)
	}
	if !hasMigrateToScript(pathB, targetV, fromV) {
		t.Error("root layout: hasMigrateToScript = false, want true")
	}

	// Layout C: absent — tarball has no migrate/ at all.
	pathC := filepath.Join(dir, "absent.tgz")
	if err := os.WriteFile(pathC, makeTarGz(t, map[string]string{
		"1.18.5/manifest.json": `{}`,
		"1.18.5/install.sh":    "#!/bin/sh\nexit 0\n",
	}), 0644); err != nil {
		t.Fatal(err)
	}
	if hasMigrateToScript(pathC, targetV, fromV) {
		t.Error("absent layout: hasMigrateToScript = true, want false")
	}

	// Layout D: false positive — file with matching basename but in a
	// different dir (e.g. payload/) must NOT be reported as a migrate/to-.
	pathD := filepath.Join(dir, "falsepos.tgz")
	if err := os.WriteFile(pathD, makeTarGz(t, map[string]string{
		"1.18.5/payload/to-1.18.9.sh": "totally-not-a-migration",
	}), 0644); err != nil {
		t.Fatal(err)
	}
	if hasMigrateToScript(pathD, targetV, fromV) {
		t.Error("false-positive layout: hasMigrateToScript = true, want false (only migrate/ entries count)")
	}

	// Layout E: version mismatch — file present but under the wrong
	// version prefix. Rolling back 1.18.9 -> 1.18.5 must NOT pick up
	// a 1.18.9/migrate/to-1.18.9.sh inside a 1.18.9 tarball.
	pathE := filepath.Join(dir, "wrongver.tgz")
	if err := os.WriteFile(pathE, makeTarGz(t, map[string]string{
		"1.18.9/migrate/to-1.18.9.sh": "self-loop-migration",
	}), 0644); err != nil {
		t.Fatal(err)
	}
	if hasMigrateToScript(pathE, targetV, fromV) {
		t.Error("wrong-version layout: hasMigrateToScript = true, want false")
	}
}

// TestExtractMigrateToScript_BothLayouts mirrors the dual-layout pattern
// used by extractTarballScript / extractManifestFromTarball. Verifies
// the extracted script has mode 0755 (so execScript can run it without
// re-chmod). targetVersion=1.18.5, fromVersion=1.18.9 — i.e. we are
// rolling back 1.18.9 -> 1.18.5 and looking for the inverse migration
// inside the 1.18.5 tarball.
func TestExtractMigrateToScript_BothLayouts(t *testing.T) {
	dir := t.TempDir()
	const targetV, fromV = "1.18.5", "1.18.9"

	// Nested layout.
	tgz := filepath.Join(dir, "nested.tgz")
	if err := os.WriteFile(tgz, makeTarGz(t, map[string]string{
		"1.18.5/migrate/to-1.18.9.sh": "#!/bin/sh\nexit 0\n",
	}), 0644); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(dir, "nested_out")
	if err := os.MkdirAll(dest, 0755); err != nil {
		t.Fatal(err)
	}
	out, err := extractMigrateToScript(tgz, targetV, fromV, dest)
	if err != nil {
		t.Fatalf("nested extract: %v", err)
	}
	if !strings.HasSuffix(out, "to-1.18.9.sh") {
		t.Errorf("out = %q, want suffix to-1.18.9.sh", out)
	}
	if st, err := os.Stat(out); err != nil {
		t.Errorf("stat: %v", err)
	} else if st.Mode()&0755 != 0755 {
		t.Errorf("mode = %v, want 0755 (executable)", st.Mode())
	}

	// Root layout.
	tgz2 := filepath.Join(dir, "root.tgz")
	if err := os.WriteFile(tgz2, makeTarGz(t, map[string]string{
		"migrate/to-1.18.9.sh": "#!/bin/sh\nexit 0\n",
	}), 0644); err != nil {
		t.Fatal(err)
	}
	dest2 := filepath.Join(dir, "root_out")
	if err := os.MkdirAll(dest2, 0755); err != nil {
		t.Fatal(err)
	}
	out2, err := extractMigrateToScript(tgz2, targetV, fromV, dest2)
	if err != nil {
		t.Fatalf("root extract: %v", err)
	}
	if !strings.HasSuffix(out2, "to-1.18.9.sh") {
		t.Errorf("out2 = %q, want suffix to-1.18.9.sh", out2)
	}
}

// TestPrintUpgradePlan_HappyPath exercises --dry-run's composable
// surface: the helpers that feed printUpgradePlan. We don't capture
// stdout (printUpgradePlan writes via fmt.Printf directly to os.Stdout
// — refactoring that for testability is out of scope for this PR), but
// we do verify readInstalledVersion is correct, which is what makes
// the "current:" line in the dry-run output trustworthy.
func TestPrintUpgradePlan_HappyPath(t *testing.T) {
	dir := t.TempDir()
	statePath := filepath.Join(dir, "state", "opencode.state.json")
	if err := os.MkdirAll(filepath.Dir(statePath), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(statePath, []byte(`{"version":"1.18.5","source":"upstream"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if got := readInstalledVersion("opencode", dir); got != "1.18.5" {
		t.Errorf("readInstalledVersion = %q, want 1.18.5", got)
	}
	if got := readInstalledVersion("missing-agent", dir); got != "" {
		t.Errorf("readInstalledVersion(missing) = %q, want empty", got)
	}

	// Also assert printUpgradePlan itself doesn't panic on the happy
	// path. It writes to os.Stdout which we accept as a side effect.
	s := &installShared{source: "upstream", channel: "stable"}
	printUpgradePlan("opencode", "1.18.9", s, dir,
		filepath.Join(dir, "cache", "opencode-upstream-1.18.9.tar.gz"),
		"sha256:deadbeef")
}

// TestExitCodeConstants asserts the new rollback constants have the
// documented values. Catches accidental renumbering during future
// refactors — an orchestrator that maps "74 == no target" would silently
// misclassify if the constant shifted.
func TestExitCodeConstants(t *testing.T) {
	cases := []struct {
		name string
		got  int
		want int
	}{
		{"ExitRollbackNoTarget", ExitRollbackNoTarget, 74},
		{"ExitRollbackTargetUnknown", ExitRollbackTargetUnknown, 75},
		{"ExitRollbackDownloadFail", ExitRollbackDownloadFail, 76},
		// Inherited codes must not drift either.
		{"ExitRenderMissing", ExitRenderMissing, 70},
		{"ExitRenderScriptError", ExitRenderScriptError, 71},
		{"ExitSystemdUnavailable", ExitSystemdUnavailable, 72},
	}
	for _, tc := range cases {
		if tc.got != tc.want {
			t.Errorf("%s = %d, want %d", tc.name, tc.got, tc.want)
		}
	}
}
