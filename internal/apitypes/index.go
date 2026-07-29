// Package apitypes defines the JSON shapes used by the marketplace HTTP API.
// These are also the shapes inside dist/index.json (the server reads index.json
// at startup and serves it via the API with possibly stripped fields).
package apitypes

// SchemaVersion is the schema version of dist/index.json.
const SchemaVersion = "1.0"

// Index is the full content of dist/index.json.
type Index struct {
	GeneratedAt string  `json:"generated_at"`
	Schema      string  `json:"schema_version"`
	Agents      []Agent `json:"agents"`
}

// Agent is one agent entry in index.json — it merges meta.yaml (display_name,
// description, icon, category, tags) with all version entries (each carrying
// a full technical manifest).
type Agent struct {
	Name        string    `json:"name"`
	DisplayName string    `json:"display_name"`
	Description string    `json:"description"`
	Icon        string    `json:"icon"`
	Category    string    `json:"category"`
	Tags        []string  `json:"tags"`
	Versions    []Version `json:"versions"`
}

// Version is one entry under Agent.Versions.
type Version struct {
	Version    string   `json:"version"`
	Source     string   `json:"source"`
	Channel    string   `json:"channel"`
	ReleasedAt string   `json:"released_at,omitempty"`
	Tarball    Tarball  `json:"tarball"`
	Manifest   Manifest `json:"manifest"`
}

// Tarball points at the actual tarball in dist/.
type Tarball struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"` // sha256:<hex>
}

// Manifest is the full technical manifest (kept inside index.json for the
// /manifest endpoint, also embedded in the tarball). See internal/manifest
// package for the canonical Go type — this is a duplicate here because the
// API JSON shape is a stable contract and should not depend on internal types.
type Manifest struct {
	SchemaVersion      string              `json:"schema_version"`
	Agent              string              `json:"agent"`
	Source             string              `json:"source"`
	Version            string              `json:"version"`
	Channel            string              `json:"channel"`
	ReleasedAt         string              `json:"released_at,omitempty"`
	Upstream           *Upstream           `json:"upstream,omitempty"`
	ForkOf             *ForkOf             `json:"fork_of,omitempty"`
	Requires           *Requires           `json:"requires"`
	RuntimeConstraints []RuntimeConstraint `json:"runtime_constraints"`
	Payload            []PayloadEntry      `json:"payload"`
	Checksums          map[string]string   `json:"checksums"`
	Tarball            *TarballRef         `json:"tarball"`
	Upgrade            *Upgrade            `json:"upgrade"`
	Scripts            *Scripts            `json:"scripts"`

	// Mirror of internal/manifest.ServiceSpec / ConfigSpec (schema 1.1+).
	Services []ServiceSpec `json:"services"`
	Configs  []ConfigSpec  `json:"configs"`
}

// ServiceSpec mirrors internal/manifest.ServiceSpec.
type ServiceSpec struct {
	Name        string   `json:"name"`
	Command     []string `json:"command"`
	Args        []string `json:"args,omitempty"`
	Restart     string   `json:"restart"`
	WorkingDir  string   `json:"working_dir,omitempty"`
	Description string   `json:"description,omitempty"`
}

// ConfigSpec mirrors internal/manifest.ConfigSpec. The RequiredInputs /
// OptionalInputs / SupportedProviders fields expose the manifest-driven
// config schema so external clients (daemon, Control UI) can introspect
// what fields each version expects without parsing manifest.json.
type ConfigSpec struct {
	Name               string                `json:"name"`
	File               string                `json:"file"`
	RenderTo           string                `json:"render_to"`
	Mode               string                `json:"mode"`
	RequiredInputs     []InputField          `json:"required_inputs,omitempty"`
	OptionalInputs     map[string]InputField `json:"optional_inputs,omitempty"`
	SupportedProviders *SupportedProviders   `json:"supported_providers,omitempty"`
}

// InputField is one input declared by an agent's manifest. See
// internal/manifest.InputField for the field semantics.
type InputField struct {
	InputKey       string   `json:"input_key,omitempty"`
	JsonPath       string   `json:"json_path"`
	Type           string   `json:"type"`
	RequiredWhen   string   `json:"required_when,omitempty"`
	EnumValues     []string `json:"enum_values,omitempty"`
	Validate       string   `json:"validate,omitempty"`
	DynamicResolve string   `json:"dynamic_resolve,omitempty"`
}

// SupportedProviders mirrors internal/manifest.SupportedProviders.
type SupportedProviders struct {
	Builtin []BuiltinProvider   `json:"builtin"`
	Custom  *CustomProviderHint `json:"custom,omitempty"`
}

// BuiltinProvider is one supported built-in LLM provider.
type BuiltinProvider struct {
	ID        string `json:"id"`
	APIKeyEnv string `json:"api_key_env,omitempty"`
	Auth      string `json:"auth,omitempty"`
	Notes     string `json:"notes,omitempty"`
}

// CustomProviderHint describes what extra inputs are needed for a
// provider id that is not in Builtin.
type CustomProviderHint struct {
	Description string `json:"description,omitempty"`
	NPMDefault  string `json:"npm_default,omitempty"`
}

type Upstream struct {
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

type Upgrade struct {
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

// IndexStripped is the response shape for /api/v1/index — it omits each
// version's full manifest to keep the response small. Frontend fetches
// individual manifests via /manifest when needed (e.g. detail view).
type IndexStripped struct {
	GeneratedAt string          `json:"generated_at"`
	Schema      string          `json:"schema_version"`
	Agents      []AgentStripped `json:"agents"`
}

type AgentStripped struct {
	Name        string            `json:"name"`
	DisplayName string            `json:"display_name"`
	Description string            `json:"description"`
	Icon        string            `json:"icon"`
	Category    string            `json:"category"`
	Tags        []string          `json:"tags"`
	Versions    []VersionStripped `json:"versions"`
}

type VersionStripped struct {
	Version    string  `json:"version"`
	Source     string  `json:"source"`
	Channel    string  `json:"channel"`
	ReleasedAt string  `json:"released_at,omitempty"`
	Tarball    Tarball `json:"tarball"`
}

// Strip returns the API response view of the full index (manifests removed
// from each version entry).
func (idx *Index) Strip() *IndexStripped {
	out := &IndexStripped{
		GeneratedAt: idx.GeneratedAt,
		Schema:      idx.Schema,
		Agents:      make([]AgentStripped, 0, len(idx.Agents)),
	}
	for _, a := range idx.Agents {
		stripped := AgentStripped{
			Name:        a.Name,
			DisplayName: a.DisplayName,
			Description: a.Description,
			Icon:        a.Icon,
			Category:    a.Category,
			Tags:        a.Tags,
			Versions:    make([]VersionStripped, 0, len(a.Versions)),
		}
		for _, v := range a.Versions {
			stripped.Versions = append(stripped.Versions, VersionStripped{
				Version:    v.Version,
				Source:     v.Source,
				Channel:    v.Channel,
				ReleasedAt: v.ReleasedAt,
				Tarball:    v.Tarball,
			})
		}
		out.Agents = append(out.Agents, stripped)
	}
	return out
}
