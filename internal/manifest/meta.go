// Package manifest also handles meta.yaml — agent-level marketing metadata
// that is version-independent (display_name, description, logo, category, tags).
//
// meta.yaml schema is documented in docs/meta-yaml-schema.md.

package manifest

import (
	"fmt"
	"net/url"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// Meta is the agent-level marketing data in agents/<name>/meta.yaml.
type Meta struct {
	DisplayName string `yaml:"display_name"`
	Description string `yaml:"description"`
	// Logo carries the agent's logo as either:
	//   - a `data:image/<mime>;base64,<payload>` URL (preferred for offline / air-gapped deployments),
	//   - an `http://` / `https://` URL (the consumer fetches it once at sync time),
	//   - a bare filename (e.g. `opencode.png`) referencing an image asset bundled in the
	//     consumer frontend (currently the agent-platform console serves them at
	//     `/marketplace-logos/<file>`), or
	//   - the empty string (the consumer falls back to its embedded default).
	//
	// Documented in docs/logo-format.md.
	Logo     string   `yaml:"logo"`
	Category string   `yaml:"category"`
	Tags     []string `yaml:"tags"`
	// RuntimeType declares the deployment target this agent ships for:
	//   - "vm"        — systemd --user on a VM / bare-metal host (default)
	//   - "container" — single-container run (the agent ships its own image / entrypoint)
	//   - "k8s"       — helm chart or k8s-manifest driven install
	//
	// Declared in meta.yaml (not manifest.json) because it is stable across
	// all versions of an agent — a k8s-targeted agent ships k8s-targeted
	// versions. See docs/meta-yaml-schema.md.
	RuntimeType string `yaml:"runtime_type"`
}

// tagRE validates tag characters.
var tagRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// staticAssetLogoRE matches a bare logo filename referencing an image asset
// bundled in the consumer frontend (e.g. the agent-platform console's
// /marketplace-logos/<file> static dir). The extension allow-list mirrors the
// backend's extForLogoMime set so the consumer never accepts a format it
// can't serve. Path-traversal guards (`/`, `..`) are checked separately so the
// regex stays a pure shape match — see validateStaticAssetFilename.
var staticAssetLogoRE = regexp.MustCompile(`^[A-Za-z0-9._-]+\.(png|jpg|jpeg|gif|webp|svg)$`)

// validRuntimeTypes is the closed enum for meta.RuntimeType. Kept here
// (not in catalogs.go) because it is a property of the agent descriptor,
// not a category lookup the user can extend.
var validRuntimeTypes = map[string]bool{
	"vm":        true,
	"container": true,
	"k8s":       true,
}

// LoadMeta reads and validates meta.yaml. Returns an error if any required
// field is missing or out of range. logo must be one of the supported formats
// (or empty); category must be in validCategories.
func LoadMeta(path string) (*Meta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read meta: %w", err)
	}
	var m Meta
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse meta: %w", err)
	}
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// Defaults fills in any unset fields with sensible defaults derived from the
// agent name. Called after validation to provide fallback values.
func (m *Meta) Defaults(agentName string) {
	if m.DisplayName == "" {
		m.DisplayName = agentName
	}
	if m.Tags == nil {
		m.Tags = []string{}
	}
	if m.RuntimeType == "" {
		m.RuntimeType = "vm"
	}
}

// Validate checks the meta against the schema.
//
// logo is optional; when present it must be one of:
//
//   - empty string (consumer falls back to default logo)
//   - `data:image/<mime>;base64,<...>` (offline / air-gapped)
//   - `http://` or `https://` URL (consumer fetches once at sync time)
//   - bare filename (consumer resolves it from its bundled frontend assets;
//     suitable only when the consumer's frontend ships that image at build time)
//
// Anything else returns a clear error. We do NOT inspect the payload bytes —
// the consumer enforces size + MIME sniffing at fetch time; we only verify
// the format here so authors get fast feedback.
func (m *Meta) Validate() error {
	if m.Description == "" {
		return fmt.Errorf("meta: description is required")
	}
	if n := len(m.Description); n < 10 || n > 200 {
		return fmt.Errorf("meta: description length %d must be between 10 and 200", n)
	}
	if err := validateLogo(m.Logo); err != nil {
		return err
	}
	if m.Category == "" {
		return fmt.Errorf("meta: category is required")
	}
	if !validCategories[m.Category] {
		return fmt.Errorf("meta: category %q is not in the catalog (see docs/category-catalog.md)", m.Category)
	}
	if m.RuntimeType != "" && !validRuntimeTypes[m.RuntimeType] {
		return fmt.Errorf("meta: runtime_type %q is not valid (want one of: vm, container, k8s)", m.RuntimeType)
	}
	if len(m.Tags) > 10 {
		return fmt.Errorf("meta: too many tags (%d, max 10)", len(m.Tags))
	}
	for _, t := range m.Tags {
		if !tagRE.MatchString(t) {
			return fmt.Errorf("meta: tag %q is invalid (must match %s)", t, tagRE)
		}
	}
	return nil
}

// validateLogo is the format-only check for meta.yaml's logo field. Format
// rules live here (not in catalogs.go) so the catalog module stays a thin
// lookup table.
func validateLogo(s string) error {
	if s == "" {
		return nil // consumer falls back to default
	}
	if strings.HasPrefix(s, "data:") {
		return validateDataURL(s)
	}
	if strings.HasPrefix(s, "http://") || strings.HasPrefix(s, "https://") {
		u, err := url.Parse(s)
		if err != nil {
			return fmt.Errorf("meta: logo %q is not a valid URL: %w", s, err)
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("meta: logo %q has unsupported scheme %q (want http/https)", s, u.Scheme)
		}
		if u.Host == "" {
			return fmt.Errorf("meta: logo %q is missing a host", s)
		}
		return nil
	}
	if validateStaticAssetFilename(s) == nil {
		return nil // bare filename referencing consumer-bundled asset
	}
	return fmt.Errorf("meta: logo %q is not a recognized format (want empty, data:image/...;base64,..., http(s) URL, or bare filename; see docs/logo-format.md)", s)
}

// validateStaticAssetFilename accepts a bare logo filename referencing an
// asset bundled in the consumer frontend (e.g. agent-platform-console's
// /marketplace-logos/<file>). The shape allow-list (extension set, charset)
// mirrors the backend's extForLogoMime so we never accept a format the
// consumer can't serve. Path-traversal guards (`/`, `..`) are checked here
// rather than baked into the regex so the regex stays a pure shape match
// that's safe to embed in error messages.
func validateStaticAssetFilename(s string) error {
	if strings.ContainsAny(s, "/\\") || strings.Contains(s, "..") {
		return fmt.Errorf("bare filename must not contain path separators")
	}
	if !staticAssetLogoRE.MatchString(s) {
		return fmt.Errorf("must match %s", staticAssetLogoRE)
	}
	return nil
}

// validateDataURL accepts `data:image/<mime>;base64,<payload>` and rejects
// anything else. We do not decode the base64 payload here — that's the
// consumer's job, and authors would otherwise be debugging payload bytes
// against a parser error.
func validateDataURL(s string) error {
	const prefix = "data:"
	rest := strings.TrimPrefix(s, prefix)
	// Expect "image/<mime>;base64,<payload>"
	semi := strings.Index(rest, ";")
	if semi < 0 || !strings.HasPrefix(rest[semi+1:], "base64,") {
		return fmt.Errorf("meta: logo data URL must be `data:image/<mime>;base64,<payload>` (got %q)", s)
	}
	mime := rest[:semi]
	if !strings.HasPrefix(mime, "image/") {
		return fmt.Errorf("meta: logo data URL must use an image/* MIME type (got %q)", mime)
	}
	// The payload segment is rest[len("base64,")+semi+1:]; we don't decode it
	// here, but we require at least one base64 char so empty bodies fail fast.
	payload := rest[semi+1+len("base64,"):]
	if payload == "" {
		return fmt.Errorf("meta: logo data URL has empty base64 payload")
	}
	return nil
}
