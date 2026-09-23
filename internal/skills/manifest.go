// Package skills provides the Go representation of a skill package — a ZIP
// file containing a SKILL.md (Markdown body + YAML frontmatter) and optional
// supporting files (scripts/, references/, assets/, workflows/). The package
// is fully independent of the agent marketplace stack (internal/manifest,
// internal/apitypes) — skills have their own on-disk layout, HTTP API, and
// CLI commands.
//
// The package has three files:
//   - manifest.go: SKILL.md frontmatter + body parsing and validation.
//   - zip.go: ZIP read/write helpers (extract SKILL.md, build from a dir).
//   - index.go: dist/skills-index.json shape and parsing.
//
// SKILL.md format is documented in docs/skill-md-schema.md.
package skills

import (
	"bytes"
	"fmt"
	"os"
	"regexp"
	"strings"
	"time"

	"golang.org/x/mod/semver"
	"gopkg.in/yaml.v3"
)

// SchemaVersion of a skill's frontmatter (mirrors the SKILL.md doc).
//
// Note: there is also an index-level schema_version (see index.go) which
// tracks dist/skills-index.json's shape. They are intentionally separate —
// SKILL.md can evolve without forcing a re-write of every skill zip.
//
// v2.0 (current): adds required Category + Agents top-level fields, plus
// optional Metadata.InstallMethod / InstallPaths / InstallConfig. The
// upgrade is breaking for the registry: any zip uploaded without
// category+agents is rejected with a 400 (invalid_manifest). Existing
// dist/skills/ files that lack these fields can still be *read* by old
// readers because schema v1.0 zips are forward-compatible at the parse
// layer — they're rejected at upload only.
const SchemaVersion = "2.0"

// Allowed source values (set at upload time, NOT in SKILL.md).
//
// `community` is the default for CLI uploads. `internal` is for official /
// in-house skills. Future expansion (partner, verified, ...) goes here.
var validSources = map[string]bool{
	"community": true,
	"internal":  true,
}

// Allowed channel values. `stable` is the default; mirrors npm dist-tags.
var validChannels = map[string]bool{
	"stable":   true,
	"beta":     true,
	"edge":     true,
	"internal": true,
}

// validInputTypes enumerates the type values allowed in inputs[].type.
var validInputTypes = map[string]bool{
	"string":  true,
	"integer": true,
	"boolean": true,
	"number":  true,
	"enum":    true,
}

// validOSNames is the closed set for metadata.requires.os[]. Unknown values
// are rejected at upload so consumers don't have to handle "linux2" etc.
var validOSNames = map[string]bool{
	"linux":   true,
	"darwin":  true,
	"windows": true,
}

// validArchNames mirrors GOARCH + a couple legacy values.
var validArchNames = map[string]bool{
	"amd64": true,
	"arm64": true,
	"386":   true,
	"arm":   true,
}

// nameRE matches a valid skill name: kebab-case, no leading/trailing hyphen,
// 1-64 chars. Mirrors the Anthropic Skills spec. The first character class
// (no hyphen) and the negative lookahead for a trailing hyphen together
// reject both leading and trailing hyphens; the second character class
// allows hyphens internally.
var nameRE = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,62}[a-z0-9])?$`)

// strictSemverRE enforces MAJOR.MINOR.PATCH[-prerelease][+build] — the full
// spec. golang.org/x/mod/semver.IsValid is too lenient: it accepts "v1" and
// "v1.0" (treating them as 1.0.0). For the registry we want strict, so the
// pre-flight regex check is done first; semver.IsValid is then used only to
// confirm the format more thoroughly and for semver.Compare later.
var strictSemverRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(?:-[0-9A-Za-z\-]+(?:\.[0-9A-Za-z\-]+)*)?(?:\+[0-9A-Za-z\-]+(?:\.[0-9A-Za-z\-]+)*)?$`)

// tagRE matches a single tag (mirrors meta.yaml's tag regex).
var tagRE = regexp.MustCompile(`^[a-z0-9-]{1,32}$`)

// homepageRE matches http(s) URLs only — no javascript:, file:, etc.
var homepageRE = regexp.MustCompile(`^https?://[^\s]+$`)

// createdAtRE matches ISO 8601 dates (YYYY-MM-DD only — no time component,
// because skill authors declare release date, not a timestamp).
var createdAtRE = regexp.MustCompile(`^[0-9]{4}-[0-9]{2}-[0-9]{2}$`)

// Manifest is the parsed SKILL.md frontmatter + body.
//
// Anthropic-compatible required fields: Name, Description.
// Registry extensions (top-level): Version, Author, Tags, CreatedAt, Homepage.
// Registry extensions grouped under Metadata: Requires, Inputs, EntryPoint,
// InstallMethod, InstallPaths, InstallConfig.
// Schema v2.0 (required): Category, Agents.
//
// Body is the Markdown content after the closing `---`; it is preserved
// verbatim so re-serialization is byte-stable (modulo trailing whitespace).
type Manifest struct {
	// === Anthropic-compatible required ===
	Name        string `yaml:"name"`
	Description string `yaml:"description"`

	// === Anthropic-compatible optional ===
	License       string   `yaml:"license,omitempty"`
	AllowedTools  []string `yaml:"allowed-tools,omitempty"`
	Compatibility string   `yaml:"compatibility,omitempty"`

	// === Registry extensions (top-level, ignored by Anthropic) ===
	Version   string   `yaml:"version,omitempty"`
	Author    string   `yaml:"author,omitempty"`
	Tags      []string `yaml:"tags,omitempty"`
	CreatedAt string   `yaml:"created_at,omitempty"`
	Homepage  string   `yaml:"homepage,omitempty"`

	// === Schema v2.0 (required) ===
	//
	// Category names the skill's functional domain. See catalogs.go for
	// the closed set. "other" is the legitimate fallback.
	Category string `yaml:"category"`
	// Agents names the runtimes that recognize this skill payload.
	// At least one entry is required; the sentinel "all" means
	// "no agent-specific install path" (used for system-level
	// installs like pip wheels or npm globals). "all" must not
	// appear alongside concrete agents.
	Agents []string `yaml:"agents"`

	// === Registry extensions under `metadata` (Anthropic allows this key) ===
	Metadata Metadata `yaml:"metadata,omitempty"`

	// Body is the raw Markdown after the frontmatter's closing `---`.
	// Populated by ParseSkillFile / LoadSkillFile / LoadSkillFileFromZip.
	Body string `yaml:"-"`
}

// Metadata groups registry-only fields under the Anthropic-allowed `metadata`
// key. Putting these here (vs. at the top level) keeps the SKILL.md
// frontmatter visually minimal for authors.
type Metadata struct {
	Requires   Requires `yaml:"requires,omitempty"`
	Inputs     []Input  `yaml:"inputs,omitempty"`
	EntryPoint string   `yaml:"entry_point,omitempty"`

	// === Schema v2.0 (optional, with sensible defaults) ===
	//
	// InstallMethod picks the install pipeline. Empty → "zip-extract"
	// (the historical behavior). See catalogs.go for the closed set.
	InstallMethod string `yaml:"install_method,omitempty"`
	// InstallPaths overrides DefaultAgentInstallPaths on a per-agent
	// basis. Keys must be agent IDs from catalogs.go; values are
	// $HOME/$NAME-substituted templates. Empty entries fall back to
	// the built-in default for each declared agent.
	InstallPaths map[string]string `yaml:"install_paths,omitempty"`
	// InstallConfig is an open-shape map[string]any whose meaning
	// depends on InstallMethod:
	//   - zip-extract: {"entry_point": "scripts/run.sh"}
	//   - pip-wheel:   {"wheel": "pkg/foo-1.0.0-py3-none-any.whl", "extras": [...]}
	//   - npm-pack:    {"package": "@scope/foo"}
	//   - tarball:     {"strip_components": 1}
	// Each install method's worker reads only the keys it understands.
	InstallConfig map[string]any `yaml:"install_config,omitempty"`
}

// Requires declares environment / tool requirements. All fields optional;
// empty arrays mean "no constraint".
type Requires struct {
	OS    []string `yaml:"os,omitempty"`
	Arch  []string `yaml:"arch,omitempty"`
	Tools []string `yaml:"tools,omitempty"`
}

// Input describes one user-facing input field. Inputs are informational —
// the runtime consumer decides whether and how to enforce the contract.
// Type must be one of validInputTypes; for `enum`, EnumValues is required.
type Input struct {
	Name        string   `yaml:"name"`
	Description string   `yaml:"description,omitempty"`
	Required    bool     `yaml:"required,omitempty"`
	Type        string   `yaml:"type"`
	EnumValues  []string `yaml:"enum_values,omitempty"`
	Default     string   `yaml:"default,omitempty"`
}

// ParseSkillFile parses the raw bytes of a SKILL.md file into a Manifest.
// The bytes must start with `---` (frontmatter delimiter); everything between
// the opening and closing `---` is YAML; everything after the closing `---`
// (and any trailing whitespace) is the Markdown body.
//
// Returns an error if:
//   - there is no opening `---`
//   - there is no closing `---` before EOF
//   - the YAML is malformed
//   - Validate() rejects the parsed manifest
func ParseSkillFile(data []byte) (*Manifest, error) {
	const delim = "---"
	// Allow leading BOM + whitespace before the first delimiter; some editors
	// (and zip tools) prepend them.
	trimmed := bytes.TrimLeft(data, " \t\r\n\xef\xbb\xbf")
	if !bytes.HasPrefix(trimmed, []byte(delim)) {
		return nil, fmt.Errorf("skills: SKILL.md must start with %q frontmatter delimiter", delim)
	}
	// Drop the opening delimiter line.
	rest := trimmed[len(delim):]
	rest = bytes.TrimLeft(rest, " \t\r\n")
	// Find the closing delimiter on its own line.
	idx := findFrontmatterClose(rest)
	if idx < 0 {
		return nil, fmt.Errorf("skills: SKILL.md frontmatter is not closed (missing %q)", delim)
	}
	frontmatterBytes := rest[:idx]
	bodyBytes := rest[idx:]
	// Strip the closing `---` line from the body.
	bodyBytes = bytes.TrimPrefix(bodyBytes, []byte(delim))
	// Strip exactly one leading newline if present (the line that contained
	// the closing `---` may have a trailing \n).
	bodyBytes = bytes.TrimPrefix(bodyBytes, []byte("\n"))
	bodyBytes = bytes.TrimPrefix(bodyBytes, []byte("\r\n"))

	var m Manifest
	if err := yaml.Unmarshal(frontmatterBytes, &m); err != nil {
		return nil, fmt.Errorf("skills: parse SKILL.md frontmatter: %w", err)
	}
	m.Body = string(bodyBytes)
	if err := m.Validate(); err != nil {
		return nil, err
	}
	return &m, nil
}

// findFrontmatterClose returns the byte offset of the closing `---` line in
// rest, or -1 if not found. The `---` must be at the start of a line and
// followed by EOL or EOF.
func findFrontmatterClose(rest []byte) int {
	const delim = "---"
	offset := 0
	for offset < len(rest) {
		nl := bytes.IndexByte(rest[offset:], '\n')
		var line []byte
		if nl < 0 {
			line = rest[offset:]
			offset = len(rest)
		} else {
			line = rest[offset : offset+nl]
			offset += nl + 1
		}
		trimmedLine := bytes.TrimSpace(line)
		if bytes.Equal(trimmedLine, []byte(delim)) {
			// offset points just past the closing newline of the `---` line.
			return offset - nl - 1
		}
	}
	return -1
}

// LoadSkillFile reads SKILL.md from a path on disk and parses it.
func LoadSkillFile(path string) (*Manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("skills: read %s: %w", path, err)
	}
	return ParseSkillFile(data)
}

// Validate enforces the frontmatter rules documented in §1.3 of the plan.
// Call this only after DefaultSource/DefaultChannel have been applied if
// you intend to fill those fields from outside (not used here — source and
// channel live in SkillVersion, not Manifest).
func (m *Manifest) Validate() error {
	// name: kebab-case, 1-64 chars, no leading/trailing hyphen.
	if m.Name == "" {
		return fmt.Errorf("skills: name is required")
	}
	if !nameRE.MatchString(m.Name) {
		return fmt.Errorf("skills: name %q is invalid (must match %s)", m.Name, nameRE)
	}
	// description: 1-1024 chars, no < or >.
	if m.Description == "" {
		return fmt.Errorf("skills: description is required")
	}
	if len(m.Description) > 1024 {
		return fmt.Errorf("skills: description length %d exceeds 1024", len(m.Description))
	}
	if strings.ContainsAny(m.Description, "<>") {
		return fmt.Errorf("skills: description must not contain < or >")
	}
	// version: strict semver (required for registry).
	if m.Version == "" {
		return fmt.Errorf("skills: version is required (must be strict semver MAJOR.MINOR.PATCH[-prerelease])")
	}
	// strictSemverRE enforces MAJOR.MINOR.PATCH explicitly (semver.IsValid
	// is too lenient — it accepts "1" and "1.0" as canonical for 1.0.0).
	// semver.IsValid is then used for an additional structural check.
	if !strictSemverRE.MatchString(m.Version) {
		return fmt.Errorf("skills: version %q is not strict semver (want MAJOR.MINOR.PATCH[-prerelease][+build])", m.Version)
	}
	if !semver.IsValid("v" + m.Version) {
		return fmt.Errorf("skills: version %q is not valid semver", m.Version)
	}
	// license: optional, ≤40 chars.
	if n := len(m.License); n > 40 {
		return fmt.Errorf("skills: license length %d exceeds 40", n)
	}
	// allowed-tools: each non-empty, ≤40 chars.
	for i, t := range m.AllowedTools {
		if t == "" {
			return fmt.Errorf("skills: allowed-tools[%d] is empty", i)
		}
		if len(t) > 40 {
			return fmt.Errorf("skills: allowed-tools[%d] %q exceeds 40 chars", i, t)
		}
	}
	// compatibility: optional, ≤500 chars.
	if n := len(m.Compatibility); n > 500 {
		return fmt.Errorf("skills: compatibility length %d exceeds 500", n)
	}
	// author: optional, ≤80 chars.
	if n := len(m.Author); n > 80 {
		return fmt.Errorf("skills: author length %d exceeds 80", n)
	}
	// tags: ≤10, each matches tagRE.
	if len(m.Tags) > 10 {
		return fmt.Errorf("skills: too many tags (%d, max 10)", len(m.Tags))
	}
	for _, t := range m.Tags {
		if !tagRE.MatchString(t) {
			return fmt.Errorf("skills: tag %q is invalid (must match %s)", t, tagRE)
		}
	}
	// created_at: ISO date.
	if m.CreatedAt != "" && !createdAtRE.MatchString(m.CreatedAt) {
		return fmt.Errorf("skills: created_at %q is not an ISO date (YYYY-MM-DD)", m.CreatedAt)
	}
	// homepage: http(s) URL.
	if m.Homepage != "" && !homepageRE.MatchString(m.Homepage) {
		return fmt.Errorf("skills: homepage %q must be an http(s) URL", m.Homepage)
	}
	// metadata.requires: each list must use valid values.
	for _, os := range m.Metadata.Requires.OS {
		if !validOSNames[os] {
			return fmt.Errorf("skills: requires.os %q is not valid (want one of: linux, darwin, windows)", os)
		}
	}
	for _, arch := range m.Metadata.Requires.Arch {
		if !validArchNames[arch] {
			return fmt.Errorf("skills: requires.arch %q is not valid (want one of: amd64, arm64, 386, arm)", arch)
		}
	}
	if len(m.Metadata.Requires.Tools) > 20 {
		return fmt.Errorf("skills: requires.tools too many entries (%d, max 20)", len(m.Metadata.Requires.Tools))
	}
	for i, t := range m.Metadata.Requires.Tools {
		if t == "" {
			return fmt.Errorf("skills: requires.tools[%d] is empty", i)
		}
	}
	// metadata.inputs: type must be valid; enum_values required for enum.
	seenNames := map[string]bool{}
	for i, in := range m.Metadata.Inputs {
		if in.Name == "" {
			return fmt.Errorf("skills: metadata.inputs[%d].name is required", i)
		}
		if seenNames[in.Name] {
			return fmt.Errorf("skills: metadata.inputs[%d].name %q is duplicated", i, in.Name)
		}
		seenNames[in.Name] = true
		if !validInputTypes[in.Type] {
			return fmt.Errorf("skills: metadata.inputs[%d].type %q is not valid (want one of: string, integer, boolean, number, enum)", i, in.Type)
		}
		if in.Type == "enum" && len(in.EnumValues) == 0 {
			return fmt.Errorf("skills: metadata.inputs[%d].type is enum but enum_values is empty", i)
		}
	}
	// metadata.entry_point: ≤200 chars. Existence check happens elsewhere
	// (the zip listing) because it requires the zip to be open.
	if n := len(m.Metadata.EntryPoint); n > 200 {
		return fmt.Errorf("skills: metadata.entry_point length %d exceeds 200", n)
	}
	// === Schema v2.0 validations ===
	//
	// category: required, must be in catalogs.go's validCategories.
	if m.Category == "" {
		return fmt.Errorf("skills: category is required (see internal/skills/catalogs.go for the closed set)")
	}
	if !IsValidCategory(m.Category) {
		return fmt.Errorf("skills: category %q is not in the catalog", m.Category)
	}
	// agents: required, ≥1 entry, every entry in validAgents.
	// "all" alone is allowed; "all" combined with concrete agents is not.
	if len(m.Agents) == 0 {
		return fmt.Errorf("skills: agents is required (at least one of: opencode, openclaw, hermes, all)")
	}
	seenAgent := map[string]bool{}
	for i, a := range m.Agents {
		if a == "" {
			return fmt.Errorf("skills: agents[%d] is empty", i)
		}
		if !IsValidAgent(a) {
			return fmt.Errorf("skills: agents[%d] %q is not in the catalog (want: opencode, openclaw, hermes, all)", i, a)
		}
		if seenAgent[a] {
			return fmt.Errorf("skills: agents[%d] %q is duplicated", i, a)
		}
		seenAgent[a] = true
	}
	if IsAllAgents(m.Agents) == false && containsAllAgent(m.Agents) {
		return fmt.Errorf("skills: 'all' cannot be combined with concrete agents (use 'all' alone)")
	}
	// install_method: optional; default "zip-extract" (filled by Defaults()).
	if m.Metadata.InstallMethod != "" && !IsValidInstallMethod(m.Metadata.InstallMethod) {
		return fmt.Errorf("skills: metadata.install_method %q is not valid (want: zip-extract, pip-wheel, npm-pack, tarball)", m.Metadata.InstallMethod)
	}
	// install_paths: optional. Each key must be a known agent (incl. "all"
	// for the universal-sentinel override); each value must contain $NAME
	// so the template expands deterministically. We don't enforce $HOME
	// presence (some setups use shared system trees) but the per-agent
	// default always uses $HOME. The "all" key is valid: when an author
	// ships `agents: [all]` with an explicit `install_paths.all`, the
	// CLI uses that override at the central-fallback path
	// (otherwise `ResolveTargets` falls back to FallbackPath). Letting
	// "all" through here means a community skill can ship a custom
	// install location without losing the "system-wide" semantic.
	for agent, pathTpl := range m.Metadata.InstallPaths {
		if !IsValidAgent(agent) {
			return fmt.Errorf("skills: metadata.install_paths key %q is not a valid agent (want: opencode, openclaw, hermes, all)", agent)
		}
		if pathTpl == "" {
			return fmt.Errorf("skills: metadata.install_paths[%s] is empty", agent)
		}
		if !strings.Contains(pathTpl, "$NAME") {
			return fmt.Errorf("skills: metadata.install_paths[%s] %q must contain $NAME placeholder", agent, pathTpl)
		}
	}
	return nil
}

// Defaults fills in optional fields with sensible values. Called after
// Validate succeeds (so we know fields are sane) but before persisting.
//
// Currently fills:
//   - CreatedAt: today's UTC date if empty.
//   - InstallMethod: "zip-extract" if empty (legacy zips stay parseable).
//   - Body: not touched (preserves whatever the author wrote).
func (m *Manifest) Defaults() {
	if m.CreatedAt == "" {
		m.CreatedAt = time.Now().UTC().Format("2006-01-02")
	}
	if m.Metadata.InstallMethod == "" {
		m.Metadata.InstallMethod = "zip-extract"
	}
}

// IsValidSource reports whether s is an allowed source value.
func IsValidSource(s string) bool { return validSources[s] }

// IsValidChannel reports whether s is an allowed channel value.
func IsValidChannel(s string) bool { return validChannels[s] }

// containsAllAgent reports whether agents contains the "all" sentinel.
// Used by Validate to reject the malformed "all + concrete agents"
// combination. Distinct from IsAllAgents (singleton check).
func containsAllAgent(agents []string) bool {
	for _, a := range agents {
		if a == "all" {
			return true
		}
	}
	return false
}
