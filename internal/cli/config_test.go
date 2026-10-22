package cli

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
)

// TestExtractRenderScriptFromTarball_BothLayouts verifies that the
// tarball extractor finds render-config.sh under either the version
// prefix or the tarball root.
func TestExtractRenderScriptFromTarball_BothLayouts(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "fake.tar.gz")
	scriptContent := "#!/usr/bin/env bash\necho render-config\n"
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"0.0.55/render-config.sh": scriptContent,
	}), 0644); err != nil {
		t.Fatalf("write nested tarball: %v", err)
	}

	// Layout 1: <version>/render-config.sh
	out1 := filepath.Join(dir, "out1.sh")
	if err := extractRenderScriptFromTarball(tarball, "0.0.55", out1); err != nil {
		t.Fatalf("extract from nested layout: %v", err)
	}
	got, err := os.ReadFile(out1)
	if err != nil {
		t.Fatalf("read out1: %v", err)
	}
	if string(got) != scriptContent {
		t.Errorf("nested layout content: got %q want %q", got, scriptContent)
	}
	if info, err := os.Stat(out1); err != nil {
		t.Fatalf("stat out1: %v", err)
	} else if info.Mode().Perm() != 0755 {
		t.Errorf("render script mode: got %o want 0755", info.Mode().Perm())
	}

	// Layout 2: render-config.sh at root
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"render-config.sh": scriptContent,
	}), 0644); err != nil {
		t.Fatalf("write root tarball: %v", err)
	}
	out2 := filepath.Join(dir, "out2.sh")
	if err := extractRenderScriptFromTarball(tarball, "0.0.55", out2); err != nil {
		t.Fatalf("extract from root layout: %v", err)
	}
}

// TestExtractManifestFromTarball_BothLayouts verifies that the manifest
// extractor finds manifest.json in either layout.
func TestExtractManifestFromTarball_BothLayouts(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "fake.tar.gz")
	const manifestContent = `{"schema_version":"1.1","agent":"opencode","source":"upstream","version":"0.0.55","channel":"stable","requires":{},"payload":[],"checksums":{},"services":[],"configs":[]}`

	// Layout 1: nested
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"0.0.55/manifest.json": manifestContent,
	}), 0644); err != nil {
		t.Fatalf("write nested tarball: %v", err)
	}
	mf, err := extractManifestFromTarball(tarball, "0.0.55")
	if err != nil {
		t.Fatalf("extract nested: %v", err)
	}
	if mf.Agent != "opencode" || mf.Version != "0.0.55" {
		t.Errorf("nested manifest: got agent=%q version=%q", mf.Agent, mf.Version)
	}
	if len(mf.Services) != 0 || len(mf.Configs) != 0 {
		t.Errorf("nested manifest: got %d services, %d configs", len(mf.Services), len(mf.Configs))
	}

	// Layout 2: root
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"manifest.json": manifestContent,
	}), 0644); err != nil {
		t.Fatalf("write root tarball: %v", err)
	}
	mf2, err := extractManifestFromTarball(tarball, "0.0.55")
	if err != nil {
		t.Fatalf("extract root: %v", err)
	}
	if mf2.Agent != "opencode" {
		t.Errorf("root manifest: got agent=%q", mf2.Agent)
	}
}

// TestExtractManifestFromTarball_NotFound verifies the error message when
// no manifest exists.
func TestExtractManifestFromTarball_NotFound(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "empty.tar.gz")
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"random.txt": "no manifest here",
	}), 0644); err != nil {
		t.Fatalf("write empty tarball: %v", err)
	}
	if _, err := extractManifestFromTarball(tarball, "0.0.55"); err == nil {
		t.Errorf("expected error when no manifest in tarball, got nil")
	}
}

// TestExtractManifestBytesFromTarball_BothLayouts verifies the bytes-only
// extractor finds manifest.json in either tarball layout. This is the
// helper used by runConfigGenerate to stage $AGENT_MARKETPLACE_MANIFEST
// for the manifest-driven render-config.sh.
func TestExtractManifestBytesFromTarball_BothLayouts(t *testing.T) {
	const want = `{"schema_version":"1.1","agent":"opencode","source":"upstream","version":"0.0.55","channel":"stable","configs":[]}`

	dir := t.TempDir()

	// Layout 1: nested
	t1 := filepath.Join(dir, "nested.tar.gz")
	if err := os.WriteFile(t1, makeTarGz(t, map[string]string{
		"0.0.55/manifest.json": want,
	}), 0644); err != nil {
		t.Fatalf("write nested tarball: %v", err)
	}
	got1, err := extractManifestBytesFromTarball(t1, "0.0.55")
	if err != nil {
		t.Fatalf("nested extract: %v", err)
	}
	if string(got1) != want {
		t.Errorf("nested: got %q want %q", got1, want)
	}

	// Layout 2: root
	t2 := filepath.Join(dir, "root.tar.gz")
	if err := os.WriteFile(t2, makeTarGz(t, map[string]string{
		"manifest.json": want,
	}), 0644); err != nil {
		t.Fatalf("write root tarball: %v", err)
	}
	got2, err := extractManifestBytesFromTarball(t2, "0.0.55")
	if err != nil {
		t.Fatalf("root extract: %v", err)
	}
	if string(got2) != want {
		t.Errorf("root: got %q want %q", got2, want)
	}
}

// TestExtractManifestBytesFromTarball_NotFound verifies the error message
// when no manifest.json is in the tarball.
func TestExtractManifestBytesFromTarball_NotFound(t *testing.T) {
	dir := t.TempDir()
	tarball := filepath.Join(dir, "empty.tar.gz")
	if err := os.WriteFile(tarball, makeTarGz(t, map[string]string{
		"random.txt": "no manifest here",
	}), 0644); err != nil {
		t.Fatalf("write empty tarball: %v", err)
	}
	if _, err := extractManifestBytesFromTarball(tarball, "0.0.55"); err == nil {
		t.Errorf("expected error when no manifest in tarball, got nil")
	}
}

// TestRenderConfigExitCodeMapping documents the exit-code contract between
// render-config.sh and agentpkg. See mapRenderExitToCLI for the full mapping.
//
// The previous version of runConfigGenerate's switch collapsed 70 to 71,
// making "required missing" indistinguishable from "type/enum error".
func TestRenderConfigExitCodeMapping(t *testing.T) {
	cases := []struct {
		name           string
		scriptExitCode int
		wantCLIExit    int
	}{
		{"required-missing (render exit 70)", 70, 70},
		{"script-error (render exit 71)", 71, 71},
		{"script-bug (render exit 1)", 1, 70},
		{"unknown (render exit 99)", 99, 71},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := mapRenderExitToCLI(tc.scriptExitCode); got != tc.wantCLIExit {
				t.Errorf("mapRenderExitToCLI(%d) = %d, want %d", tc.scriptExitCode, got, tc.wantCLIExit)
			}
		})
	}
}
// TestSlotEnvVars_EnvNameAndPayload verifies the two halves of the
// render-config.sh contract that slotEnvVars implements:
//
//  1. Env var name = MARKETPLACE_CONFIG_ + upper(slotName) with '-' → '_'.
//     (hermes-agent's "hermes-env" slot must become ..._HERMES_ENV.)
//  2. Env var value = base64(JSON) of the SLOT BODY, not the wrapper. The
//     scripts `base64 -d` the value and write it verbatim to render_to, so
//     encoding the wrapper would nest the slot key into the rendered file.
func TestSlotEnvVars_EnvNameAndPayload(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "config-input.json")
	body := `{"hermes":{"model":{"provider":"custom:litellm","default":"qwen3.5-9b"}},"hermes-env":{"HERMES_QUIET":false}}`
	if err := os.WriteFile(inputPath, []byte(body), 0644); err != nil {
		t.Fatalf("write input: %v", err)
	}

	cfgs := []manifest.ConfigSpec{
		{Name: "hermes", File: "config.yaml", RenderTo: "~/.hermes/config.yaml", Mode: "0600"},
		{Name: "hermes-env", File: ".env", RenderTo: "~/.hermes/.env", Mode: "0600"},
	}
	env, err := slotEnvVars(inputPath, cfgs)
	if err != nil {
		t.Fatalf("slotEnvVars: %v", err)
	}

	got := map[string]string{}
	for _, kv := range env {
		parts := strings.SplitN(kv, "=", 2)
		got[parts[0]] = parts[1]
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 env vars, got %d: %v", len(got), env)
	}

	// Env var names.
	if _, ok := got["MARKETPLACE_CONFIG_HERMES"]; !ok {
		t.Errorf("missing MARKETPLACE_CONFIG_HERMES; got %v", got)
	}
	if _, ok := got["MARKETPLACE_CONFIG_HERMES_ENV"]; !ok {
		t.Errorf("missing MARKETPLACE_CONFIG_HERMES_ENV (dash must become underscore); got %v", got)
	}

	// Payload = slot body, base64-decoded back to compact JSON.
	for name, want := range map[string]string{
		"MARKETPLACE_CONFIG_HERMES":     `{"model":{"provider":"custom:litellm","default":"qwen3.5-9b"}}`,
		"MARKETPLACE_CONFIG_HERMES_ENV": `{"HERMES_QUIET":false}`,
	} {
		decoded, err := base64.StdEncoding.DecodeString(got[name])
		if err != nil {
			t.Fatalf("decode %s: %v", name, err)
		}
		if string(decoded) != want {
			t.Errorf("%s payload = %s, want %s (slot body, not wrapper)", name, decoded, want)
		}
	}
}

// TestSlotEnvVars_MissingSlot verifies a declared slot absent from the
// config-input is a hard error. The render scripts signal the same class of
// failure with their `:?` guard, and agentpkg maps it to exit 70 — so we
// fail before exec rather than letting the script exit 1 (also mapped to 70
// but with a far less specific message).
func TestSlotEnvVars_MissingSlot(t *testing.T) {
	dir := t.TempDir()
	inputPath := filepath.Join(dir, "config-input.json")
	if err := os.WriteFile(inputPath, []byte(`{"hermes":{}}`), 0644); err != nil {
		t.Fatalf("write input: %v", err)
	}
	cfgs := []manifest.ConfigSpec{
		{Name: "hermes", File: "config.yaml", RenderTo: "~/.hermes/config.yaml", Mode: "0600"},
		{Name: "hermes-env", File: ".env", RenderTo: "~/.hermes/.env", Mode: "0600"},
	}
	if _, err := slotEnvVars(inputPath, cfgs); err == nil {
		t.Errorf("expected error when a declared slot is missing, got nil")
	}
}

// TestSlotEnvVars_DrivesRealRenderScript is the end-to-end proof of the
// agentpkg → render-config.sh contract: the env vars slotEnvVars produces are
// exactly what the shipped render script consumes, and the bytes that land in
// render_to are the slot body (not the wrapper).
//
// This runs the agent's REAL render-config.sh out of the source tree. The
// scripts are first-party in this repo — nothing external is executed.
func TestSlotEnvVars_DrivesRealRenderScript(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("jq"); err != nil {
		t.Skip("jq not available; render-config.sh requires it")
	}

	cases := []struct {
		name       string
		agentDir   string
		version    string
		slot       string
		slotBody   string
		renderTo   string // relative to fake HOME
		envVarName string
	}{
		{
			name:       "opencode",
			agentDir:   "opencode",
			version:    "1.19.0",
			slot:       "opencode",
			slotBody:   `{"model":"litellm/qwen3.5-9b","provider":{"litellm":{"npm":"@ai-sdk/openai-compatible","options":{"baseURL":"http://gw:4000/v1"},"models":{"qwen3.5-9b":{"name":"qwen3.5-9b"}}}}}`,
			renderTo:   ".config/opencode/opencode.json",
			envVarName: "MARKETPLACE_CONFIG_OPENCODE",
		},
		{
			name:       "openclaw",
			agentDir:   "openclaw",
			version:    "2026.7.1-2",
			slot:       "openclaw",
			slotBody:   `{"gateway":{"mode":"local","port":18789},"models":{"mode":"replace","providers":{"litellm":{"baseUrl":"http://gw:4000/v1","models":[{"id":"m","name":"m"}]}}}}`,
			renderTo:   ".openclaw/openclaw.json",
			envVarName: "MARKETPLACE_CONFIG_OPENCLAW",
		},
	}

	repoRoot, err := filepath.Abs("../..")
	if err != nil {
		t.Fatalf("abs repo root: %v", err)
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			script := filepath.Join(repoRoot, "agents", tc.agentDir, "upstream", tc.version, "render-config.sh")
			if _, err := os.Stat(script); err != nil {
				t.Skipf("render script not present: %v", err)
			}

			home := t.TempDir()
			inputPath := filepath.Join(t.TempDir(), "config-input.json")
			// config-input is the slot-keyed wrapper, exactly like config/example.*
			wrapper := `{"` + tc.slot + `":` + tc.slotBody + `}`
			if err := os.WriteFile(inputPath, []byte(wrapper), 0644); err != nil {
				t.Fatalf("write input: %v", err)
			}

			env, err := slotEnvVars(inputPath, []manifest.ConfigSpec{
				{Name: tc.slot, File: "x.json", RenderTo: "~/x.json", Mode: "0600"},
			})
			if err != nil {
				t.Fatalf("slotEnvVars: %v", err)
			}
			if !strings.HasPrefix(env[0], tc.envVarName+"=") {
				t.Fatalf("env var = %q, want prefix %q", env[0], tc.envVarName+"=")
			}

			cmd := exec.Command("bash", script)
			cmd.Env = append(os.Environ(),
				"HOME="+home,
				"AGENT_MARKETPLACE_AGENT="+tc.agentDir,
				"AGENT_MARKETPLACE_VERSION="+tc.version,
				env[0],
			)
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("render-config.sh failed: %v\n%s", err, out)
			}

			got, err := os.ReadFile(filepath.Join(home, tc.renderTo))
			if err != nil {
				t.Fatalf("read rendered file: %v", err)
			}
			// The rendered file must be the SLOT BODY. If agentpkg encoded the
			// wrapper we would see {"<slot>":{...}} instead.
			var rendered map[string]json.RawMessage
			if err := json.Unmarshal(got, &rendered); err != nil {
				t.Fatalf("rendered file is not JSON: %v\n%s", err, got)
			}
			if _, nested := rendered[tc.slot]; nested {
				t.Errorf("rendered file still contains the slot wrapper key %q — "+
					"agentpkg must encode the slot BODY, not the wrapper; got %s", tc.slot, got)
			}
			if !json.Valid([]byte(tc.slotBody)) {
				t.Fatalf("test fixture slotBody is not valid JSON")
			}
		})
	}

	// hermes-agent is the multi-slot case: two slots, two output formats
	// (YAML + KEY=VALUE .env), and a slot name containing a dash
	// ("hermes-env") that must become an underscore in the env var. It
	// produces two env vars and two rendered files, so it doesn't fit the
	// single-slot table above. The dash→underscore transform is covered
	// directly by TestSlotEnvVars_EnvNameAndPayload; this case asserts the
	// dash-carrying slot name also survives the full path.
	hermesScript := filepath.Join(repoRoot, "agents", "hermes-agent", "upstream", "0.19.0", "render-config.sh")
	if _, err := os.Stat(hermesScript); err != nil {
		t.Logf("hermes render script not present, skipping multi-slot case: %v", err)
		return
	}
	hermesHome := t.TempDir()
	hermesInput := filepath.Join(t.TempDir(), "config-input.json")
	// Mirrors config/example.yaml: two slots, each a plain object.
	if err := os.WriteFile(hermesInput,
		[]byte(`{"hermes":{"model":{"provider":"custom:litellm","default":"qwen3.5-9b"}},"hermes-env":{"HERMES_QUIET":false,"HERMES_API_TIMEOUT":60}}`),
		0644); err != nil {
		t.Fatalf("write hermes input: %v", err)
	}
	hermesEnv, err := slotEnvVars(hermesInput, []manifest.ConfigSpec{
		{Name: "hermes", File: "config.yaml", RenderTo: "~/.hermes/config.yaml", Mode: "0600"},
		{Name: "hermes-env", File: ".env", RenderTo: "~/.hermes/.env", Mode: "0600"},
	})
	if err != nil {
		t.Fatalf("slotEnvVars (hermes): %v", err)
	}
	if len(hermesEnv) != 2 {
		t.Fatalf("hermes: expected 2 env vars, got %d", len(hermesEnv))
	}
	if !strings.HasPrefix(hermesEnv[1], "MARKETPLACE_CONFIG_HERMES_ENV=") {
		t.Errorf("hermes: second env var = %q, want MARKETPLACE_CONFIG_HERMES_ENV=", hermesEnv[1])
	}

	// The YAML slot needs a writer (yq, or python3+PyYAML) reachable from the
	// script's OWN environment. The probe MUST use the same HOME the script
	// will see: python3 resolves its user-site from $HOME, so a probe under the
	// ambient HOME can succeed while the script — which we run with an isolated
	// temp HOME — still fails to import yaml.
	hermesScriptEnv := append(os.Environ(),
		"HOME="+hermesHome,
		"AGENT_MARKETPLACE_AGENT=hermes-agent",
		"AGENT_MARKETPLACE_VERSION=0.19.0",
	)
	haveYAMLWriter := false
	if _, yqErr := exec.LookPath("yq"); yqErr == nil {
		haveYAMLWriter = true
	} else {
		probe := exec.Command("python3", "-c", "import yaml")
		probe.Env = hermesScriptEnv
		if out, pyErr := probe.CombinedOutput(); pyErr == nil {
			haveYAMLWriter = true
		} else {
			t.Logf("no YAML writer for the hermes slot under an isolated HOME; "+
				"verifying only the hermes-env slot. Install yq or PyYAML for full coverage: %s", out)
		}
	}

	if haveYAMLWriter {
		hcmd := exec.Command("bash", hermesScript)
		hcmd.Env = hermesScriptEnv
		hcmd.Env = append(hcmd.Env, hermesEnv...)
		if out, runErr := hcmd.CombinedOutput(); runErr != nil {
			t.Fatalf("hermes render-config.sh failed: %v\n%s", runErr, out)
		}
		// hermes renders as YAML; assert the file exists and carries the provider.
		yamlFile, err := os.ReadFile(filepath.Join(hermesHome, ".hermes", "config.yaml"))
		if err != nil {
			t.Fatalf("read hermes config.yaml: %v", err)
		}
		if !strings.Contains(string(yamlFile), "custom:litellm") {
			t.Errorf("hermes config.yaml missing provider; got:\n%s", yamlFile)
		}
		return
	}

	// No YAML writer for the hermes slot. Still assert the hermes-env payload —
	// the dash→underscore slot name produced a base64 body that decodes to the
	// slot body (not the wrapper), which is exactly what the script consumes.
	env0 := hermesEnv[1] // MARKETPLACE_CONFIG_HERMES_ENV=...
	kv := strings.SplitN(env0, "=", 2)
	decoded, err := base64.StdEncoding.DecodeString(kv[1])
	if err != nil {
		t.Fatalf("decode hermes-env payload: %v", err)
	}
	var body map[string]any
	if err := json.Unmarshal(decoded, &body); err != nil {
		t.Fatalf("hermes-env payload is not a JSON object: %v", err)
	}
	if _, nested := body["hermes-env"]; nested {
		t.Errorf("hermes-env payload contains the wrapper key: %s", decoded)
	}
	if body["HERMES_QUIET"] != false {
		t.Errorf("hermes-env payload HERMES_QUIET = %v, want false", body["HERMES_QUIET"])
	}
}
