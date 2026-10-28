// Package manifest provides the Go representation of manifest.json
// inside an agent tarball. Used by:
//   - agentpkg package verify / build (client-side validation)
//   - marketplace-api startup validation (server-side integrity check)
//   - agentpkg install (extract payload/runtime_requirements)
package manifest

import (
	"encoding/json"
	"fmt"
	"os"
)

// Manifest is the full content of manifest.json inside a tarball.
type Manifest struct {
	SchemaVersion string `json:"schema_version"`
	Agent         string `json:"agent"`
	Source        string `json:"source"`
	Version       string `json:"version"`
	Channel       string `json:"channel"`
	ReleasedAt    string `json:"released_at,omitempty"`

	Upstream *UpstreamRef `json:"upstream,omitempty"`
	ForkOf   *ForkOf      `json:"fork_of,omitempty"`

	Requires           *Requires           `json:"requires"`
	RuntimeRequirements []RuntimeRequirement `json:"runtime_requirements"`
	Payload            []PayloadEntry      `json:"payload"`

	Checksums map[string]string `json:"checksums"`

	Tarball *TarballRef `json:"tarball"`

	Upgrade *UpgradeSpec `json:"upgrade"`
	Scripts *Scripts     `json:"scripts"`

	// Services (added in schema_version 1.1) describe systemd --user units
	// that agentpkg should write and enable after install.sh runs. Each unit
	// has a fixed Command + Restart policy; configuration lives in the
	// Configs[] entries below, not here.
	Services []ServiceSpec `json:"services"`

	// Configs (added in schema_version 1.1) describes configuration files
	// that the agent's own render-config.sh produces. agentpkg does NOT
	// generate these files — it only verifies them after the render script
	// runs (Stage 1 of the daemon-driven install flow).
	Configs []ConfigSpec `json:"configs"`
}

// ServiceSpec describes a single systemd --user unit that agentpkg should
// install after the agent's install.sh completes. The Command is the
// argv[0..N] that systemd will exec; WorkingDirectory is an absolute path
// (typically {{DEPLOY_ROOT}}); Restart follows systemd's Restart= spec.
type ServiceSpec struct {
	Name        string   `json:"name"`
	Command     []string `json:"command"`
	Args        []string `json:"args,omitempty"`
	Restart     string   `json:"restart"`                 // on-failure|always|no
	WorkingDir  string   `json:"working_dir,omitempty"`
	Description string   `json:"description,omitempty"`
}

// ConfigSpec describes a single configuration file produced by the agent's
// render-config.sh script. The script owns writing the file at RenderTo
// (chmod 0600 by convention for sensitive files); agentpkg only verifies
// the file appeared.
//
// This is the DELIVERY contract only — where each config file lands and under
// what permissions. The SHAPE contract (what fields the rendered file may
// contain) lives in the agent's `config/schema.json` (draft-07 JSON Schema),
// shipped alongside the manifest in the same package. The two are related by
// name: `Name` must match a top-level key under the schema's `properties`, and
// that key's value is the schema for this slot.
//
// `File` is the basename the script should produce in its output directory
// (informational — agentpkg does not write the file itself).
// `RenderTo` is the final on-host path (e.g. "~/.openclaw/openclaw.json").
// `Mode` is informational; the script is responsible for chmod.
//
// SupportedProviders exposes the catalog of built-in providers an agent can be
// configured against, so the platform can stamp `x-provider-choices` onto the
// schema's provider fields. It is zero-valued for manifests that declare no
// provider catalog.
type ConfigSpec struct {
	Name               string              `json:"name"`
	File               string              `json:"file"`
	RenderTo           string              `json:"render_to"`
	Mode               string              `json:"mode"`
	SupportedProviders *SupportedProviders `json:"supported_providers,omitempty"`
}

// SupportedProviders exposes the catalog of built-in providers an agent
// can be configured against, plus a hint for custom (OpenAI-compatible)
// providers. CustomPathRequired lists the keys that must be supplied
// when the user picks a provider id outside Builtin.
type SupportedProviders struct {
	Builtin []BuiltinProvider  `json:"builtin"`
	Custom  *CustomProviderHint `json:"custom,omitempty"`
}

// BuiltinProvider is one supported built-in LLM provider.
type BuiltinProvider struct {
	ID        string `json:"id"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	// Auth is "api_key" (default) or "oauth" — only oauth providers may
	// legitimately omit api_key_env.
	Auth    string `json:"auth,omitempty"`
	Notes   string `json:"notes,omitempty"`
}

// CustomProviderHint describes what extra inputs are needed to configure
// a provider id that is not in Builtin (treated as an OpenAI-compatible
// endpoint).
type CustomProviderHint struct {
	Description string `json:"description,omitempty"`
	NPMDefault  string `json:"npm_default,omitempty"`
}

type UpstreamRef struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	URL     string `json:"url"`
	SHA256  string `json:"sha256"`
	Notes   string `json:"notes,omitempty"`
}

type ForkOf struct {
	Agent   string `json:"agent"`
	Version string `json:"version"`
	Source  string `json:"source"`
}

type Requires struct {
	OS             []string `json:"os"`
	Arch           []string `json:"arch"`
	SystemPackages []string `json:"system_packages"`
	SystemTools    []string `json:"system_tools"`
}

type RuntimeRequirement struct {
	Name        string `json:"name"`
	// Version is a constraint expression understood by the agent's
	// install.sh (e.g. ">=22.22.3 <23, >=24.15.0 <25", ">=3.12,<3.14").
	// Tools (tools/install-runtime.sh) parse this with jq + capture.
	Version     string `json:"version"`
	InstallHint string `json:"install_hint,omitempty"`
	VerifyCmd   string `json:"verify_cmd,omitempty"`
}

type PayloadEntry struct {
	Src  string `json:"src"`
	Dst  string `json:"dst"`
	Mode string `json:"mode"`
}

type TarballRef struct {
	Filename string `json:"filename"`
	SHA256   string `json:"sha256"`
}

type UpgradeSpec struct {
	Strategy       string           `json:"strategy"`
	CompatibleFrom []string         `json:"compatible_from"`
	Migrations     []MigrationEntry `json:"migrations"`
}

type MigrationEntry struct {
	From   string `json:"from"`
	Script string `json:"script"`
}

type Scripts struct {
	Install   string `json:"install"`
	Uninstall string `json:"uninstall"`
}

// Load reads manifest.json from a file path.
func Load(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read manifest: %w", err)
	}
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}

// LoadFromBytes parses manifest.json from in-memory bytes.
func LoadFromBytes(data []byte) (*Manifest, error) {
	var m Manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse manifest: %w", err)
	}
	return &m, nil
}
