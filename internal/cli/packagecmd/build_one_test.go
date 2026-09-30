package packagecmd

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestBuildOne_TarballNameUsesManifestAgent is the regression for F025:
//
// the previous buildOne derived the tarball filename from
// ``filepath.Base(agentDir)``, not from ``manifest.agent``. So:
//
//   $ agentpkg package init my-test-agent --dir /tmp/pkg-init
//   $ agentpkg package build /tmp/pkg-init --version 0.1.0
//   # wrote /tmp/pkg-init/dist/pkg-init-upstream-0.1.0.tar.gz
//   # (and dist/index.json with agent="my-test-agent", so the
//   #    on-disk filename and the index entry disagreed)
//
// The fix reads identity from ``manifest.json`` so the filename matches
// the index entry and the rest of the registry. This test renames the
// scaffolded directory and asserts the build still emits a tarball
// named after the manifest's agent field, not the dir's basename.
func TestBuildOne_TarballNameUsesManifestAgent(t *testing.T) {
	root := t.TempDir()

	// 1. Scaffold an agent named "my-test-agent" inside a directory
	//    called "different-dir-name" — the dir-name vs. manifest-name
	//    mismatch is exactly the F025 scenario.
	scaffoldDir := filepath.Join(root, "different-dir-name")
	if err := scaffoldAgent("my-test-agent", scaffoldDir); err != nil {
		t.Fatalf("scaffoldAgent: %v", err)
	}

	// 2. Move the directory one more time, to a third name, so we're
	//    confident buildOne isn't reading the *original* scaffold
	//    path. The manifest inside is unchanged.
	renamedDir := filepath.Join(root, "another-name")
	if err := os.Rename(scaffoldDir, renamedDir); err != nil {
		t.Fatalf("rename: %v", err)
	}

	// 3. Build it. Output goes to <outDir> (explicit, to avoid the
	//    F022 default-resolution path — we want to isolate F025 here).
	outDir := t.TempDir()
	if err := buildOne(renamedDir, "upstream", "stable", "0.1.0", outDir, false); err != nil {
		t.Fatalf("buildOne: %v", err)
	}

	// 4. The tarball must be named after the manifest's agent field
	//    ("my-test-agent"), NOT after either dir basename
	//    ("different-dir-name" or "another-name").
	wantName := "my-test-agent-upstream-0.1.0.tar.gz"
	wantPath := filepath.Join(outDir, wantName)
	if _, err := os.Stat(wantPath); err != nil {
		t.Errorf("tarball missing at %s: %v (look at %s for what was produced)",
			wantPath, err, outDir)
	}

	// Belt-and-braces: the dist/index.json entry must also use the
	// manifest's agent field (it already did before the fix; we check
	// here so a future regression that re-introduces the dir-name
	// coupling fails loudly).
	idxData, err := os.ReadFile(filepath.Join(outDir, "index.json"))
	if err != nil {
		t.Fatalf("read index.json: %v", err)
	}
	var idx struct {
		Agents []struct {
			Name string `json:"name"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(idxData, &idx); err != nil {
		t.Fatalf("parse index.json: %v\n%s", err, idxData)
	}
	if len(idx.Agents) != 1 || idx.Agents[0].Name != "my-test-agent" {
		t.Errorf("index.json agent name = %+v, want [my-test-agent]\n%s",
			idx.Agents, idxData)
	}
}

// TestBuildOne_DefaultOutLandsNextToAgent is the F022 half:
//
// the previous --out default was "dist" relative to the process CWD,
// so `package build /tmp/pkg-init` from $HOME wrote to
// $HOME/dist/<...>.tar.gz. The fix resolves the empty default to
// <agent-dir>/dist so the output lands next to the scaffolded dir.
//
// (The skills-build equivalent lives in skillscmd_test.go — same
// default-resolution logic, separate tests so each bug's regression
// is independent.)
func TestBuildOne_DefaultOutLandsNextToAgent(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "my-test-agent")
	if err := scaffoldAgent("my-test-agent", agentDir); err != nil {
		t.Fatalf("scaffoldAgent: %v", err)
	}

	// Pretend the operator is at a different directory. This is the
	// case where the old CWD-relative default produced output at CWD
	// instead of next to the agent dir.
	otherCwd := t.TempDir()
	origCwd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(otherCwd); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(origCwd) }()

	if err := buildOne(agentDir, "upstream", "stable", "0.1.0", "", false); err != nil {
		t.Fatalf("buildOne: %v", err)
	}

	wantTarball := filepath.Join(agentDir, "dist", "my-test-agent-upstream-0.1.0.tar.gz")
	if _, err := os.Stat(wantTarball); err != nil {
		t.Errorf("tarball missing at %s: %v", wantTarball, err)
	}
	wrongTarball := filepath.Join(otherCwd, "dist", "my-test-agent-upstream-0.1.0.tar.gz")
	if _, err := os.Stat(wrongTarball); err == nil {
		t.Errorf("tarball incorrectly landed at CWD-relative path %s", wrongTarball)
	}
}

// TestBuildOne_RejectsEmptyManifestAgent is a defensive check: if the
// upstream operator hand-edits the scaffolded manifest and empties out
// the ``agent`` field, buildOne must refuse rather than emit a
// tarball with an empty stem (which would collide with any other
// agent's tarball on reindex).
func TestBuildOne_RejectsEmptyManifestAgent(t *testing.T) {
	root := t.TempDir()
	agentDir := filepath.Join(root, "my-test-agent")
	if err := scaffoldAgent("my-test-agent", agentDir); err != nil {
		t.Fatalf("scaffoldAgent: %v", err)
	}

	// Wipe the agent field in manifest.json. We rewrite via json
	// round-trip so we keep the rest of the schema intact.
	mfPath := filepath.Join(agentDir, "upstream", "0.1.0", "manifest.json")
	data, err := os.ReadFile(mfPath)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatal(err)
	}
	m["agent"] = ""
	patched, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(mfPath, patched, 0644); err != nil {
		t.Fatal(err)
	}

	outDir := t.TempDir()
	err = buildOne(agentDir, "upstream", "stable", "0.1.0", outDir, false)
	if err == nil {
		t.Fatal("expected error for empty manifest.agent, got nil")
	}
	if !contains(err.Error(), "agent") {
		t.Errorf("error message should mention 'agent' field, got: %v", err)
	}
}

// contains is a tiny helper so the test file doesn't need to import
// strings just for one substring check.
func contains(haystack, needle string) bool {
	for i := 0; i+len(needle) <= len(haystack); i++ {
		if haystack[i:i+len(needle)] == needle {
			return true
		}
	}
	return false
}