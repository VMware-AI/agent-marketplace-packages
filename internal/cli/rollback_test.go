package cli

import (
	"bytes"
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

	cur, targetV, targetSrc, targetCh, err := readStatePrevious("opencode", dir)
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
	// state.json in this fixture does NOT carry .previous.channel —
	// agentpkg must accept it gracefully (targetChannel == "").
	if targetCh != "" {
		t.Errorf("targetChannel = %q, want empty (pre-1.2 state.json)", targetCh)
	}
}

// TestReadStatePrevious_ChannelPresent exercises the schema 1.2
// extension where .previous also records the channel the prior version
// was on. When present it must come back verbatim — this is what lets
// agentpkg rollback avoid the --channel override for newer installs.
func TestReadStatePrevious_ChannelPresent(t *testing.T) {
	dir := t.TempDir()
	state := map[string]any{
		"agent":   "opencode",
		"source":  "upstream",
		"version": "1.19.0",
		"channel": "beta",
		"previous": map[string]any{
			"version": "1.18.9",
			"source":  "upstream",
			"channel": "beta",
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
	_, _, _, targetCh, err := readStatePrevious("opencode", dir)
	if err != nil {
		t.Fatalf("readStatePrevious: %v", err)
	}
	if targetCh != "beta" {
		t.Errorf("targetChannel = %q, want beta", targetCh)
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
			_, _, _, _, err := readStatePrevious("opencode", d)
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

// TestResolveInverseMigration_Wildcards covers the priority scheme
// documented in docs/upgrade-protocol.md:39-42 — exact match wins over
// major.minor wildcard, which wins over major wildcard. Tests both the
// nested and root layouts, plus a "no match" case.
func TestResolveInverseMigration_Wildcards(t *testing.T) {
	dir := t.TempDir()
	const targetV, fromV = "1.18.5", "1.18.9"

	cases := []struct {
		name     string
		entries  map[string]string
		wantBase string
		wantOK   bool
	}{
		{
			name:     "exact match wins over wildcards",
			entries:  map[string]string{"1.18.5/migrate/to-1.18.9.sh": "x", "1.18.5/migrate/to-1.18.x.sh": "x", "1.18.5/migrate/to-1.x.x.sh": "x"},
			wantBase: "to-1.18.9.sh",
			wantOK:   true,
		},
		{
			name:     "major.minor wildcard when no exact match",
			entries:  map[string]string{"1.18.5/migrate/to-1.18.x.sh": "x", "1.18.5/migrate/to-1.x.x.sh": "x"},
			wantBase: "to-1.18.x.sh",
			wantOK:   true,
		},
		{
			name:     "major wildcard as last resort",
			entries:  map[string]string{"1.18.5/migrate/to-1.x.x.sh": "x"},
			wantBase: "to-1.x.x.sh",
			wantOK:   true,
		},
		{
			name:     "root-layout wildcard accepted",
			entries:  map[string]string{"migrate/to-1.18.x.sh": "x"},
			wantBase: "to-1.18.x.sh",
			wantOK:   true,
		},
		{
			name:    "no match returns false",
			entries: map[string]string{"1.18.5/migrate/from-1.18.9.sh": "x"}, // wrong prefix
			wantOK:  false,
		},
		{
			name:    "empty tarball returns false",
			entries: map[string]string{},
			wantOK:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.name+".tgz")
			if err := os.WriteFile(path, makeTarGz(t, tc.entries), 0644); err != nil {
				t.Fatal(err)
			}
			gotBase, gotOK := resolveInverseMigration(path, targetV, fromV)
			if gotOK != tc.wantOK {
				t.Errorf("matched = %v, want %v", gotOK, tc.wantOK)
			}
			if gotBase != tc.wantBase {
				t.Errorf("basename = %q, want %q", gotBase, tc.wantBase)
			}
		})
	}
}

// TestPlanRollbackJSON_HappyPath captures stdout, invokes planRollbackJSON
// for a synthetic state.json, and asserts the resulting JSON shape: both
// "current" and "target" blocks populated, channel resolved, exit code 0.
//
// We redirect os.Stdout via os.Pipe; the planRollbackJSON helper uses
// fmt.Println, which writes to os.Stdout, so this is the standard
// capture pattern in pure Go.
func TestPlanRollbackJSON_HappyPath(t *testing.T) {
	dir := t.TempDir()
	state := map[string]any{
		"agent":   "opencode",
		"source":  "upstream",
		"version": "1.18.9",
		"channel": "stable",
		"previous": map[string]any{
			"version": "1.18.5",
			"source":  "upstream",
			"channel": "stable",
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

	s := &installShared{targetRoot: dir}
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	oldStdout := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = oldStdout }()

	planErr := planRollbackJSON("opencode", s, "", "")
	_ = w.Close()
	if planErr != nil {
		t.Fatalf("planRollbackJSON: %v", planErr)
	}

	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("read pipe: %v", err)
	}
	out := buf.String()

	var got map[string]any
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("plan output is not valid JSON: %v\nraw: %s", err, out)
	}
	if got["agent"] != "opencode" {
		t.Errorf("agent = %v, want opencode", got["agent"])
	}
	cur, ok := got["current"].(map[string]any)
	if !ok {
		t.Fatalf("current block missing or wrong type: %v", got["current"])
	}
	if cur["version"] != "1.18.9" || cur["source"] != "upstream" {
		t.Errorf("current = %v, want version=1.18.9 source=upstream", cur)
	}
	tgt, ok := got["target"].(map[string]any)
	if !ok {
		t.Fatalf("target block missing or wrong type: %v", got["target"])
	}
	if tgt["version"] != "1.18.5" || tgt["source"] != "upstream" || tgt["channel"] != "stable" {
		t.Errorf("target = %v, want version=1.18.5 source=upstream channel=stable", tgt)
	}
	im, ok := got["inverse_migration"].(map[string]any)
	if !ok {
		t.Fatalf("inverse_migration block missing")
	}
	if im["expected_script_basename"] != "to-1.18.9.sh" {
		t.Errorf("expected_script_basename = %v, want to-1.18.9.sh", im["expected_script_basename"])
	}
}

// TestPlanRollbackJSON_NoTarget asserts that planRollbackJSON surfaces
// the same exit-code semantics as runRollback when state.json has no
// usable .previous block — the orchestrator gets a typed rollbackError
// (code 74) instead of an empty JSON object.
func TestPlanRollbackJSON_NoTarget(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "state", "opencode.state.json"),
		[]byte(`{"version":"1.18.9"}`), 0644); err != nil {
		t.Fatal(err)
	}
	err := planRollbackJSON("opencode", &installShared{}, "", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var rb *rollbackError
	if !errors.As(err, &rb) {
		t.Fatalf("err type %T, want *rollbackError", err)
	}
	if rb.code != ExitRollbackNoTarget {
		t.Errorf("code = %d, want %d", rb.code, ExitRollbackNoTarget)
	}
}

// TestSplitSemver pins the wildcard-candidate builder behavior. The
// helpers downstream depend on first.two.dotted.parts being extracted
// verbatim; non-semver inputs must be rejected so we don't try to match
// wildcards against, e.g., "dev" or "1".
func TestSplitSemver(t *testing.T) {
	cases := []struct {
		in       string
		major    string
		minor    string
		ok       bool
	}{
		{"1.18.9", "1", "18", true},
		{"2.0.0", "2", "0", true},
		// Suffixes after patch are ignored; only first two dotted parts
		// matter for wildcard resolution.
		{"1.18.9-rc1", "1", "18", true},
		{"10.20.30-beta.1", "10", "20", true},
		// Edge cases — reject.
		{"1", "", "", false},
		{"", "", "", false},
		{".18.9", "", "", false},
		{"1..9", "", "", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			major, minor, ok := splitSemver(tc.in)
			if major != tc.major || minor != tc.minor || ok != tc.ok {
				t.Errorf("splitSemver(%q) = (%q, %q, %v), want (%q, %q, %v)",
					tc.in, major, minor, ok, tc.major, tc.minor, tc.ok)
			}
		})
	}
}
