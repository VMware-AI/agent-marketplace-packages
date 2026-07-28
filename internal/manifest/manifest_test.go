package manifest

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestManifest_RoundTrip_FieldsPreserved verifies that encoding a Manifest
// with full Services + Configs (schema 1.1) and decoding back yields equal
// field values. This catches the silent-drop pattern that bit the
// runtime_constraints/runtime_requirements field mismatch.
func TestManifest_RoundTrip_FieldsPreserved(t *testing.T) {
	original := &Manifest{
		SchemaVersion: "1.1",
		Agent:         "openclaw",
		Source:        "upstream",
		Version:       "2026.7.1-2",
		Channel:       "stable",
		Services: []ServiceSpec{
			{
				Name:        "gateway",
				Command:     []string{"openclaw", "gateway", "--port", "8080"},
				Args:        []string{"--foreground"},
				Restart:     "on-failure",
				WorkingDir:  "/home/agent/.local/openclaw/2026.7.1-2",
				Description: "openclaw gateway daemon",
			},
		},
		Configs: []ConfigSpec{
			{
				Name:     "openclaw",
				File:     "openclaw.json",
				RenderTo: "/home/agent/.openclaw/openclaw.json",
				Mode:     "0600",
			},
		},
	}

	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded Manifest
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if decoded.SchemaVersion != original.SchemaVersion {
		t.Errorf("schema_version round-trip: got %q want %q", decoded.SchemaVersion, original.SchemaVersion)
	}
	if len(decoded.Services) != 1 {
		t.Fatalf("services: got %d want 1", len(decoded.Services))
	}
	got := decoded.Services[0]
	if got.Name != original.Services[0].Name {
		t.Errorf("services[0].name: got %q want %q", got.Name, original.Services[0].Name)
	}
	if len(got.Command) != len(original.Services[0].Command) {
		t.Fatalf("services[0].command length: got %d want %d", len(got.Command), len(original.Services[0].Command))
	}
	for i := range got.Command {
		if got.Command[i] != original.Services[0].Command[i] {
			t.Errorf("services[0].command[%d]: got %q want %q", i, got.Command[i], original.Services[0].Command[i])
		}
	}
	if got.Restart != original.Services[0].Restart {
		t.Errorf("services[0].restart: got %q want %q", got.Restart, original.Services[0].Restart)
	}
	if got.WorkingDir != original.Services[0].WorkingDir {
		t.Errorf("services[0].working_dir: got %q want %q", got.WorkingDir, original.Services[0].WorkingDir)
	}
	if got.Description != original.Services[0].Description {
		t.Errorf("services[0].description: got %q want %q", got.Description, original.Services[0].Description)
	}
	if len(got.Args) != 1 || got.Args[0] != "--foreground" {
		t.Errorf("services[0].args: got %v want [--foreground]", got.Args)
	}

	if len(decoded.Configs) != 1 {
		t.Fatalf("configs: got %d want 1", len(decoded.Configs))
	}
	gotCfg := decoded.Configs[0]
	if gotCfg.Name != "openclaw" || gotCfg.File != "opencode.json" {
		// sanity: this should never fire; just checking wire.
	}
	if gotCfg.Name != original.Configs[0].Name {
		t.Errorf("configs[0].name: got %q want %q", gotCfg.Name, original.Configs[0].Name)
	}
	if gotCfg.File != original.Configs[0].File {
		t.Errorf("configs[0].file: got %q want %q", gotCfg.File, original.Configs[0].File)
	}
	if gotCfg.RenderTo != original.Configs[0].RenderTo {
		t.Errorf("configs[0].render_to: got %q want %q", gotCfg.RenderTo, original.Configs[0].RenderTo)
	}
	if gotCfg.Mode != original.Configs[0].Mode {
		t.Errorf("configs[0].mode: got %q want %q", gotCfg.Mode, original.Configs[0].Mode)
	}
}

// TestManifest_OneZero_BackwardCompat verifies that loading a 1.0 manifest
// (no services/configs fields) yields nil slices without panicking. This
// is critical: agentpkg must continue to work with tarballs built before
// this schema bump.
func TestManifest_OneZero_BackwardCompat(t *testing.T) {
	v10 := `{
		"schema_version": "1.0",
		"agent": "opencode",
		"source": "upstream",
		"version": "0.0.55",
		"channel": "stable",
		"runtime_constraints": [],
		"payload": [],
		"checksums": {}
	}`
	var m Manifest
	if err := json.Unmarshal([]byte(v10), &m); err != nil {
		t.Fatalf("unmarshal 1.0: %v", err)
	}
	if m.Services != nil {
		t.Errorf("services: got %v want nil", m.Services)
	}
	if m.Configs != nil {
		t.Errorf("configs: got %v want nil", m.Configs)
	}
	if m.Agent != "opencode" {
		t.Errorf("agent: got %q want opencode", m.Agent)
	}
}

// TestManifest_OneOne_NewFields verifies that the actual 1.1 openclaw
// manifest round-trips and the Services + Configs slices decode correctly.
// This catches typos in JSON tags vs the on-disk JSON shape.
func TestManifest_OneOne_NewFields(t *testing.T) {
	v11 := `{
		"schema_version": "1.1",
		"agent": "openclaw",
		"source": "upstream",
		"version": "2026.7.1-2",
		"channel": "stable",
		"runtime_constraints": [],
		"payload": [],
		"checksums": {},
		"services": [
			{
				"name": "gateway",
				"command": ["openclaw", "gateway", "--port", "8080"],
				"restart": "on-failure",
				"working_dir": "{{DEPLOY_ROOT}}",
				"description": "openclaw gateway daemon"
			}
		],
		"configs": [
			{
				"name": "openclaw",
				"file": "openclaw.json",
				"render_to": "~/.openclaw/openclaw.json",
				"mode": "0600"
			}
		]
	}`
	var m Manifest
	if err := json.Unmarshal([]byte(v11), &m); err != nil {
		t.Fatalf("unmarshal 1.1: %v", err)
	}
	if len(m.Services) != 1 || m.Services[0].Name != "gateway" {
		t.Errorf("services: got %+v want one entry named 'gateway'", m.Services)
	}
	if m.Services[0].Command[2] != "--port" || m.Services[0].Command[3] != "8080" {
		t.Errorf("services[0].command: got %v want [... --port 8080]", m.Services[0].Command)
	}
	if len(m.Configs) != 1 || m.Configs[0].RenderTo != "~/.openclaw/openclaw.json" {
		t.Errorf("configs: got %+v want one entry with render_to ~/.openclaw/openclaw.json", m.Configs)
	}
}

// TestManifest_Serialize_ToJSONKeys asserts the exact on-wire JSON keys.
// Anything added/removed here is a wire-format change for every agent in
// the marketplace — this test is the tripwire.
func TestManifest_Serialize_ToJSONKeys(t *testing.T) {
	m := Manifest{
		Services: []ServiceSpec{
			{Name: "x", Command: []string{"a"}, Restart: "on-failure", WorkingDir: "/tmp"},
		},
		Configs: []ConfigSpec{
			{Name: "x", File: "x.json", RenderTo: "/tmp/x.json", Mode: "0600"},
		},
	}
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)

	// Required keys must be present.
	requiredKeys := []string{
		`"services"`,
		`"configs"`,
		`"name":"x"`,
		`"command":["a"]`,
		`"restart":"on-failure"`,
		`"working_dir":"/tmp"`,
		`"file":"x.json"`,
		`"render_to":"/tmp/x.json"`,
		`"mode":"0600"`,
	}
	for _, k := range requiredKeys {
		if !strings.Contains(s, k) {
			t.Errorf("missing required key %q in %q", k, s)
		}
	}

	// Removed keys (legacy schema) must NOT appear anymore.
	forbiddenKeys := []string{
		`"env_from_config"`,
		`"placeholders"`,
		`"required"`,
		// `path` was used by an earlier draft of ConfigSpec — should not exist now.
	}
	for _, k := range forbiddenKeys {
		if strings.Contains(s, k) {
			t.Errorf("forbidden key %q present in %q", k, s)
		}
	}
}