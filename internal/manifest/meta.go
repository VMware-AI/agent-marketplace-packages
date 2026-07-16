// Package manifest also handles meta.yaml — agent-level marketing metadata
// that is version-independent (display_name, description, icon, category, tags).
//
// meta.yaml schema is documented in docs/meta-yaml-schema.md.

package manifest

import (
	"fmt"
	"os"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Meta is the agent-level marketing data in agents/<name>/meta.yaml.
type Meta struct {
	DisplayName string   `yaml:"display_name"`
	Description string   `yaml:"description"`
	Icon        string   `yaml:"icon"`
	Category    string   `yaml:"category"`
	Tags        []string `yaml:"tags"`
}

// iconRE validates tag characters.
var tagRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// LoadMeta reads and validates meta.yaml. Returns an error if any required
// field is missing or out of range. icon must be in validIcons; category must
// be in validCategories.
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

// Validate checks the meta against the catalog constraints.
func (m *Meta) Validate() error {
	if m.Description == "" {
		return fmt.Errorf("meta: description is required")
	}
	if n := len(m.Description); n < 10 || n > 200 {
		return fmt.Errorf("meta: description length %d must be between 10 and 200", n)
	}
	if m.Icon == "" {
		return fmt.Errorf("meta: icon is required")
	}
	if !validIcons[m.Icon] {
		return fmt.Errorf("meta: icon %q is not in the catalog (see docs/icon-catalog.md)", m.Icon)
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
