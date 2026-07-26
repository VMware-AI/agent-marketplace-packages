// Package manifest provides the Go representation of manifest.json
// inside an agent tarball. Used by:
//   - agentpkg package verify / build (client-side validation)
//   - marketplace-api startup validation (server-side integrity check)
//   - agentpkg install (extract payload/runtime_constraints)
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
	RuntimeConstraints []RuntimeConstraint `json:"runtime_constraints"`
	Payload            []PayloadEntry      `json:"payload"`

	Checksums map[string]string `json:"checksums"`

	Tarball *TarballRef `json:"tarball"`

	Upgrade *UpgradeSpec `json:"upgrade"`
	Scripts *Scripts     `json:"scripts"`
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

type RuntimeConstraint struct {
	Name       string `json:"name"`
	MinVersion string `json:"min_version"`
	MaxVersion string `json:"max_version,omitempty"`
	UseSystem  bool   `json:"use_system"`
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
