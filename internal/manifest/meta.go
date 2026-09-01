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
	//   - an `http://` / `https://` URL (the consumer fetches it once at sync time), or
	//   - the empty string (the consumer falls back to its embedded default).
	//
	// Documented in docs/logo-format.md.
	Logo     string   `yaml:"logo"`
	Category string   `yaml:"category"`
	Tags     []string `yaml:"tags"`
}

// tagRE validates tag characters.
var tagRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

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
}

// Validate checks the meta against the schema.
//
// logo is optional; when present it must be one of:
//
//   - empty string (consumer falls back to default logo)
//   - `data:image/<mime>;base64,<...>` (offline / air-gapped)
//   - `http://` or `https://` URL (consumer fetches once at sync time)
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
	return fmt.Errorf("meta: logo %q is not a recognized format (want empty, data:image/...;base64,..., or http(s) URL; see docs/logo-format.md)", s)
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
