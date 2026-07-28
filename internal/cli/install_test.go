package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
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
