package cli

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
)

// TestWriteSystemdUnit_PathSubstitution verifies that {{DEPLOY_ROOT}} in
// the unit's WorkingDirectory and Command is substituted with the
// absolute deploy_root path.
func TestWriteSystemdUnit_PathSubstitution(t *testing.T) {
	svc := manifest.ServiceSpec{
		Name:        "gateway",
		Command:     []string{"openclaw", "gateway", "--port", "8080"},
		Restart:     "on-failure",
		WorkingDir:  "{{DEPLOY_ROOT}}",
		Description: "test svc",
	}
	deployRoot := "/home/agent/.local/openclaw/2026.7.1-2"
	body := serviceSpecToUnitFile("openclaw", svc, deployRoot)

	if !strings.Contains(body, "WorkingDirectory=/home/agent/.local/openclaw/2026.7.1-2") {
		t.Errorf("WorkingDirectory not substituted: %s", body)
	}
	if !strings.Contains(body, "ExecStart=openclaw gateway --port 8080") {
		t.Errorf("ExecStart missing: %s", body)
	}
	if !strings.Contains(body, "Description=test svc") {
		t.Errorf("Description missing: %s", body)
	}
	if !strings.Contains(body, "Restart=on-failure") {
		t.Errorf("Restart missing: %s", body)
	}
	if !strings.Contains(body, "[Install]") {
		t.Errorf("Unit file missing [Install] section: %s", body)
	}
}

// TestWriteSystemdUnit_DefaultWorkingDir verifies that when WorkingDir
// is empty, it defaults to {{DEPLOY_ROOT}}.
func TestWriteSystemdUnit_DefaultWorkingDir(t *testing.T) {
	svc := manifest.ServiceSpec{
		Name:    "gateway",
		Command: []string{"openclaw", "gateway"},
		Restart: "on-failure",
	}
	deployRoot := "/home/agent/.local/openclaw/2026.7.1-2"
	body := serviceSpecToUnitFile("openclaw", svc, deployRoot)
	if !strings.Contains(body, "WorkingDirectory="+deployRoot) {
		t.Errorf("default WorkingDirectory not set to deploy_root: %s", body)
	}
}

// TestAppendStateJsonFields verifies that postInstall's state.json
// augmentation adds services/configs/config_dir without losing existing
// fields.
func TestAppendStateJsonFields(t *testing.T) {
	dir := t.TempDir()
	targetRoot := filepath.Join(dir, ".local")
	if err := os.MkdirAll(filepath.Join(targetRoot, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(targetRoot, "state", "openclaw.state.json")
	original := map[string]any{
		"agent":         "openclaw",
		"source":        "upstream",
		"version":       "2026.7.1-2",
		"channel":       "stable",
		"deploy_root":   "/home/agent/.local/openclaw/2026.7.1-2",
		"target_root":   targetRoot,
		"installed_files": []string{"bin/openclaw", "../bin/openclaw"},
	}
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	mf := &manifest.Manifest{
		Services: []manifest.ServiceSpec{
			{Name: "gateway", Restart: "on-failure"},
		},
		Configs: []manifest.ConfigSpec{
			{Name: "openclaw", RenderTo: "~/.openclaw/openclaw.json", Mode: "0600"},
		},
	}
	svcs := []stateService{
		{Name: "gateway", UnitPath: "/home/agent/.config/systemd/user/openclaw-gateway.service", Started: true},
	}

	if err := appendStateJsonFields(targetRoot, "openclaw", "/home/agent/.local/openclaw/2026.7.1-2", mf, svcs); err != nil {
		t.Fatalf("appendStateJsonFields: %v", err)
	}

	// Read back and verify.
	got, err := os.ReadFile(stateFile)
	if err != nil {
		t.Fatal(err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(got, &parsed); err != nil {
		t.Fatal(err)
	}

	// Existing fields preserved.
	if parsed["agent"] != "openclaw" {
		t.Errorf("agent: got %v want openclaw", parsed["agent"])
	}
	if parsed["version"] != "2026.7.1-2" {
		t.Errorf("version: got %v want 2026.7.1-2", parsed["version"])
	}

	// New fields added.
	services, ok := parsed["services"].([]any)
	if !ok || len(services) != 1 {
		t.Fatalf("services: got %v want one entry", parsed["services"])
	}
	gotSvc := services[0].(map[string]any)
	if gotSvc["name"] != "gateway" || gotSvc["started"] != true {
		t.Errorf("service entry: got %+v", gotSvc)
	}

	configs, ok := parsed["configs"].([]any)
	if !ok || len(configs) != 1 {
		t.Fatalf("configs: got %v want one entry", parsed["configs"])
	}
	gotCfg := configs[0].(map[string]any)
	if gotCfg["name"] != "openclaw" || gotCfg["render_to"] != "~/.openclaw/openclaw.json" {
		t.Errorf("config entry: got %+v", gotCfg)
	}

	if parsed["config_dir"] != "/home/agent/.local/openclaw/2026.7.1-2/config" {
		t.Errorf("config_dir: got %v", parsed["config_dir"])
	}
}

// TestAppendStateJsonFields_Idempotent verifies that re-running
// appendStateJsonFields on the same file (e.g. after a failed-then-retried
// install) replaces the services/configs fields rather than duplicating
// or concatenating them.
func TestAppendStateJsonFields_Idempotent(t *testing.T) {
	dir := t.TempDir()
	targetRoot := filepath.Join(dir, ".local")
	if err := os.MkdirAll(filepath.Join(targetRoot, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(targetRoot, "state", "openclaw.state.json")
	original := map[string]any{"agent": "openclaw", "version": "2026.7.1-2"}
	data, _ := json.Marshal(original)
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	mf := &manifest.Manifest{
		Services: []manifest.ServiceSpec{{Name: "gateway", Restart: "on-failure"}},
		Configs:  []manifest.ConfigSpec{{Name: "openclaw", RenderTo: "/x/y", Mode: "0600"}},
	}
	svcs := []stateService{{Name: "gateway", UnitPath: "/u/g.service", Started: true}}

	// First call.
	if err := appendStateJsonFields(targetRoot, "openclaw", "/d", mf, svcs); err != nil {
		t.Fatal(err)
	}
	// Second call (should replace, not duplicate).
	if err := appendStateJsonFields(targetRoot, "openclaw", "/d", mf, svcs); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(stateFile)
	var parsed map[string]any
	_ = json.Unmarshal(got, &parsed)

	if services, _ := parsed["services"].([]any); len(services) != 1 {
		t.Errorf("services after 2 calls: got %d want 1", len(services))
	}
	if configs, _ := parsed["configs"].([]any); len(configs) != 1 {
		t.Errorf("configs after 2 calls: got %d want 1", len(configs))
	}
}

// TestAppendStateJsonFields_NoServices verifies that passing noServices=true
// preserves the existing services[] in state.json (in case --no-services
// was passed on a retry after a partial install).
func TestAppendStateJsonFields_NoServices(t *testing.T) {
	dir := t.TempDir()
	targetRoot := filepath.Join(dir, ".local")
	if err := os.MkdirAll(filepath.Join(targetRoot, "state"), 0755); err != nil {
		t.Fatal(err)
	}
	stateFile := filepath.Join(targetRoot, "state", "openclaw.state.json")
	original := map[string]any{
		"agent": "openclaw",
		"services": []map[string]any{
			{"name": "gateway", "unit_path": "/u/g.service", "started": true},
		},
	}
	data, _ := json.Marshal(original)
	if err := os.WriteFile(stateFile, data, 0644); err != nil {
		t.Fatal(err)
	}

	// Empty services list → preserve existing.
	if err := appendStateJsonFields(targetRoot, "openclaw", "/d",
		&manifest.Manifest{
			Services: []manifest.ServiceSpec{{Name: "gateway", Restart: "on-failure"}},
		},
		nil, // empty svcs (simulates --no-services)
	); err != nil {
		t.Fatal(err)
	}

	got, _ := os.ReadFile(stateFile)
	var parsed map[string]any
	_ = json.Unmarshal(got, &parsed)

	services, _ := parsed["services"].([]any)
	if len(services) != 1 {
		t.Errorf("services preserved when noServices: got %d want 1", len(services))
	}
}

// TestSubstituteVars verifies basic {{KEY}} substitution.
func TestSubstituteVars(t *testing.T) {
	got := substituteVars("WorkingDirectory={{DEPLOY_ROOT}}", map[string]string{"DEPLOY_ROOT": "/x/y"})
	if got != "WorkingDirectory=/x/y" {
		t.Errorf("got %q", got)
	}
}

// TestRmdirEmptyParents exercises the parent-chain cleanup used by
// runUninstall after deleting a rendered config or systemd unit. We
// cover the four paths that actually matter:
//
//  1. empty parent → rmdir, walk up
//  2. non-empty parent → stop, preserve it
//  3. stopAt reached → stop, preserve it
//  4. file-not-found path → no-op, no crash
func TestRmdirEmptyParents(t *testing.T) {
	dir := t.TempDir()
	home := filepath.Join(dir, "home")
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}

	// Case 1: chain of empty dirs — should rmdir both opencode/ and its
	// parent .config/ (the chain stops at $HOME).
	cfgDir := filepath.Join(home, ".config", "opencode")
	cfgFile := filepath.Join(cfgDir, "opencode.json")
	if err := os.MkdirAll(cfgDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgFile, []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}
	// Remove the file ourselves (the helper is meant to be called AFTER
	// os.Remove of the leaf); simulate by removing the file too.
	if err := os.Remove(cfgFile); err != nil {
		t.Fatal(err)
	}
	if got := rmdirEmptyParents(cfgFile, home); got != filepath.Join(home, ".config") {
		t.Errorf("case 1: got %q want %q", got, filepath.Join(home, ".config"))
	}
	if _, err := os.Stat(cfgDir); !os.IsNotExist(err) {
		t.Errorf("case 1: %s should be gone", cfgDir)
	}
	if _, err := os.Stat(filepath.Join(home, ".config")); !os.IsNotExist(err) {
		t.Errorf("case 1: .config parent should also be gone (empty)")
	}

	// Case 2: non-empty parent stops the walk. Create .config with two
	// children; remove only one, expect the other to keep the dir alive.
	mixed := filepath.Join(home, ".config")
	if err := os.MkdirAll(filepath.Join(mixed, "systemd"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(mixed, "systemd", "user-a.service"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	leaf := filepath.Join(mixed, "systemd", "user-b.service")
	if err := os.WriteFile(leaf, nil, 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(leaf); err != nil {
		t.Fatal(err)
	}
	if got := rmdirEmptyParents(leaf, home); got != "" {
		t.Errorf("case 2: got %q want \"\" (non-empty parent should stop walk)", got)
	}
	if _, err := os.Stat(filepath.Join(mixed, "systemd")); err != nil {
		t.Errorf("case 2: systemd dir should still exist")
	}

	// Case 3: stopAt is exclusive — never remove it.
	if err := os.RemoveAll(filepath.Join(mixed, "systemd")); err != nil {
		t.Fatal(err)
	}
	// .config is now empty; rmdirEmptyParents should NOT delete .config
	// if it's the stopAt. (We pass mixed as stopAt here to test the bound.)
	if got := rmdirEmptyParents(filepath.Join(mixed, "anything"), mixed); got != "" {
		t.Errorf("case 3: rmdir walked past stopAt: got %q", got)
	}
	if _, err := os.Stat(mixed); err != nil {
		t.Errorf("case 3: stopAt itself was removed (must be exclusive)")
	}

	// Case 4: leaf doesn't exist — function still returns "" cleanly.
	missing := filepath.Join(home, "nope", "missing.json")
	if got := rmdirEmptyParents(missing, home); got != "" {
		t.Errorf("case 4: got %q want \"\" for missing path", got)
	}
}

// TestWrapInstallScriptExit_PropagatesExit10 is the F015 regression:
//
// install.sh's "already installed" path exits 10 (a documented semantic —
// see docs/install-protocol.md / hermes-agent's install.sh line 137).
// The previous runInstall swallowed that code and bubbled up a generic
// error, so the agentpkg binary exited 1 while the message said
// "exit 10" — orchestrators couldn't reliably distinguish "duplicate
// install" from a real failure. The fix detects *exec.ExitError and
// re-emits as an ExitCoder carrying the script's exit code.
//
// We don't stand up a real tarball + install.sh here — the wrap is a
// pure function of (err, name, version) → error. Running a synthetic
// sh -c "exit N" captures the *exec.ExitError that install.sh would
// produce.
func TestWrapInstallScriptExit_PropagatesExit10(t *testing.T) {
	// exec.Command's Run() returns an *exec.ExitError on non-zero
	// exit — the same shape install.sh produces when it exits 10
	// for "already installed". Run sh -c "exit 10" to capture one.
	err := exec.Command("sh", "-c", "exit 10").Run()
	if exitErr, ok := err.(*exec.ExitError); !ok || exitErr.ExitCode() != 10 {
		t.Fatalf("test setup: expected *exec.ExitError with code 10, got %T %v", err, err)
	}

	wrapped := wrapInstallScriptExit(err, "hermes-agent", "0.19.0")
	if wrapped == nil {
		t.Fatal("wrapInstallScriptExit returned nil")
	}

	// The wrapped error must implement ExitCoder with code 10, so
	// main.go's errors.As path translates to os.Exit(10) — matching
	// the install.sh's semantic.
	var ec ExitCoder
	if !errorsAs(wrapped, &ec) {
		t.Fatalf("wrapped error should implement ExitCoder; got %T: %v", wrapped, wrapped)
	}
	if ec.ExitCode() != 10 {
		t.Errorf("exit code = %d, want 10", ec.ExitCode())
	}

	// The message must mention agent + version + exit code so
	// operators can read stderr and immediately tell what happened.
	msg := wrapped.Error()
	for _, want := range []string{"hermes-agent", "0.19.0", "10"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}

// TestWrapInstallScriptExit_PropagatesOtherCodes confirms the wrap
// preserves arbitrary non-zero exit codes (e.g. install.sh exits 50
// for checksum mismatch per docs/install-protocol.md). Without this,
// every non-zero script exit would collapse to agentpkg's default 1.
func TestWrapInstallScriptExit_PropagatesOtherCodes(t *testing.T) {
	for _, code := range []int{20, 30, 40, 50, 60, 70, 99} {
		err := exec.Command("sh", "-c", "exit "+strconv.Itoa(code)).Run()
		exitErr, ok := err.(*exec.ExitError)
		if !ok {
			t.Fatalf("code=%d: expected *exec.ExitError, got %T", code, err)
		}
		if exitErr.ExitCode() != code {
			t.Fatalf("code=%d: setup got exit %d", code, exitErr.ExitCode())
		}
		wrapped := wrapInstallScriptExit(err, "opencode", "1.18.9")
		var ec ExitCoder
		if !errorsAs(wrapped, &ec) {
			t.Fatalf("code=%d: wrapped error should implement ExitCoder; got %T", code, wrapped)
		}
		if ec.ExitCode() != code {
			t.Errorf("code=%d: ExitCode() = %d, want %d", code, ec.ExitCode(), code)
		}
	}
}

// TestWrapInstallScriptExit_PassesThroughNonExitError confirms that
// errors that aren't *exec.ExitError (e.g. tar extraction failure)
// pass through unchanged — they have no script exit code to
// propagate, and we shouldn't fabricate one.
func TestWrapInstallScriptExit_PassesThroughNonExitError(t *testing.T) {
	plain := errorString("some other failure (no script exit code)")
	got := wrapInstallScriptExit(plain, "opencode", "1.18.9")
	if got != plain {
		t.Errorf("non-ExitError should pass through unchanged; got %v", got)
	}
}

// TestWrapInstallScriptExit_NilReturnsNil is the trivial nil → nil
// case so callers can use the wrap unconditionally without a nil check.
func TestWrapInstallScriptExit_NilReturnsNil(t *testing.T) {
	if got := wrapInstallScriptExit(nil, "x", "1.0.0"); got != nil {
		t.Errorf("nil → nil; got %v", got)
	}
}

// errorString is a tiny newtype to satisfy the error interface for the
// "non-ExitError" test case. Plain string would not work because the
// test wants to compare identity (the wrap should pass it through
// unchanged).
type errorString string

func (e errorString) Error() string { return string(e) }
