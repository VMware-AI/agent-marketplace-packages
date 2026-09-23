package skills

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"

	"golang.org/x/mod/semver"
)

// IndexSchemaVersion is the schema version of dist/skills-index.json.
// Bumped only when the index JSON shape changes in a way old readers
// would mis-interpret (renames, type changes, required-field additions).
//
// v2.0 (current): Skill gains `category`; SkillVersion gains `agents`,
// `install_method`, `install_paths`, `install_config`. Existing v1.0
// readers will still parse the JSON (unknown fields are ignored), so
// dist/skills-index.json from before this change is forward-compatible
// on read. Reverse direction requires a reindex after the registry
// rolls out v2.0 — uploads without category/agents will be rejected at
// the server (see internal/skills/manifest.go SchemaVersion).
const IndexSchemaVersion = "2.0"

// Index is the full content of dist/skills-index.json — the registry's
// self-describing snapshot of every published skill and version.
//
// JSON tags use snake_case to match the agent apitypes.Agent shape; the
// HTTP API serves IndexStripped (see below) for list views and the full
// Index (or per-skill Skill) for detail views.
type Index struct {
	GeneratedAt string  `json:"generated_at"`
	Schema      string  `json:"schema_version"`
	Skills      []Skill `json:"skills"`
}

// Skill is one skill entry in the index. It mixes "skill-level" fields
// (stable across all versions: name, author, tags, category) with a list
// of versions, each carrying its own (source, channel, agents, install
// method, install paths, install config, zip reference, body).
//
// Skills from different sources (community vs internal) live as separate
// top-level Skill entries even if they share a name — URL design guarantees
// they have distinct identities on disk.
type Skill struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description"`
	Author      string `json:"author,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	License     string `json:"license,omitempty"`
	Homepage    string `json:"homepage,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`

	// === Schema v2.0 ===
	// Category names the skill's functional domain (what it does).
	// Stable across versions; declared once in SKILL.md and copied
	// into every version's parent Skill struct at upload time.
	Category string `json:"category"`

	Versions []SkillVersion `json:"versions"`
}

// SkillVersion is one (source, channel, version) tuple under a Skill.
type SkillVersion struct {
	Version    string    `json:"version"`
	Source     string    `json:"source"`
	Channel    string    `json:"channel"`
	ReleasedAt string    `json:"released_at,omitempty"`
	Requires   *Requires `json:"requires,omitempty"`
	Inputs     []Input   `json:"inputs,omitempty"`
	EntryPoint string    `json:"entry_point,omitempty"`

	// === Schema v2.0 ===
	//
	// Agents lists the runtimes that recognize this skill payload.
	// ["all"] means system-level (pip wheel / npm global).
	// Empty list falls back to the all-sentinel at the install layer.
	Agents []string `json:"agents"`
	// InstallMethod picks the install pipeline
	// (zip-extract / pip-wheel / npm-pack / tarball).
	InstallMethod string `json:"install_method"`
	// InstallPaths is the author-supplied override of
	// DefaultAgentInstallPaths on a per-agent basis. Keys are agent IDs;
	// values are $HOME/$NAME-substituted templates.
	InstallPaths map[string]string `json:"install_paths,omitempty"`
	// InstallConfig is open-shape per-method configuration. Consumers
	// interpret based on InstallMethod.
	InstallConfig map[string]any `json:"install_config,omitempty"`

	Zip Zip `json:"zip"`
	// Body is the full SKILL.md text (frontmatter + body) for this version.
	// Embedding it lets the UI render cards and detail views without ever
	// unzipping the artifact. The canonical bytes still live in the zip;
	// fetch them via GET /api/v1/skills/{source}/{name}/{version}/SKILL.md.
	Body string `json:"body,omitempty"`
}

// Zip points at the actual artifact in dist/skills/.
//
// SizeBytes is included so a UI can show "1.2 MB" without a HEAD request.
// SHA256 uses the `sha256:<hex>` format for consistency with agent tarballs.
type Zip struct {
	Filename  string `json:"filename"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

// IndexStripped is the lightweight list-view projection. It drops per-version
// Body, Inputs, Requires, and EntryPoint — just enough to render a card grid.
// The server projects to this shape when the client requests /api/v1/skills.
type IndexStripped struct {
	GeneratedAt string          `json:"generated_at"`
	Schema      string          `json:"schema_version"`
	Skills      []SkillStripped `json:"skills"`
}

// SkillStripped is the per-skill projection for list views.
type SkillStripped struct {
	Name        string                `json:"name"`
	DisplayName string                `json:"display_name,omitempty"`
	Description string                `json:"description"`
	Author      string                `json:"author,omitempty"`
	Tags        []string              `json:"tags,omitempty"`
	License     string                `json:"license,omitempty"`
	Homepage    string                `json:"homepage,omitempty"`
	CreatedAt   string                `json:"created_at,omitempty"`
	// Schema v2.0:
	Category string                  `json:"category"`
	Versions []SkillVersionStripped  `json:"versions"`
}

// SkillVersionStripped is the per-version projection for list views — same
// as SkillVersion but omits Body, Inputs, Requires, EntryPoint.
//
// Schema v2.0 retains Agents/InstallMethod/InstallPaths at the list view
// level because the UI's "install method" column is a primary list-view
// concern. InstallConfig is dropped — it's only meaningful at install time.
type SkillVersionStripped struct {
	Version    string `json:"version"`
	Source     string `json:"source"`
	Channel    string `json:"channel"`
	ReleasedAt string `json:"released_at,omitempty"`
	// Schema v2.0:
	Agents        []string          `json:"agents"`
	InstallMethod string            `json:"install_method"`
	InstallPaths  map[string]string `json:"install_paths,omitempty"`
	Zip           Zip               `json:"zip"`
}

// ParseIndex loads dist/skills-index.json into an Index. The file MUST exist;
// missing-file callers should catch os.ErrNotExist and treat as empty.
func ParseIndex(path string) (*Index, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return ParseIndexBytes(data)
}

// ParseIndexBytes is the in-memory variant of ParseIndex, exposed for tests
// and for callers that already have the bytes in hand (e.g. the upload
// handler rebuilding the index after a successful write).
func ParseIndexBytes(data []byte) (*Index, error) {
	var idx Index
	if err := json.Unmarshal(data, &idx); err != nil {
		return nil, fmt.Errorf("skills: parse index: %w", err)
	}
	if idx.Schema == "" {
		idx.Schema = IndexSchemaVersion
	}
	return &idx, nil
}

// MarshalIndex serializes the index back to JSON in the same shape ParseIndex
// reads. The two-space indent matches the agent index writer
// (internal/cli/packagecmd/build_index.go) for consistency.
func MarshalIndex(idx *Index) ([]byte, error) {
	return json.MarshalIndent(idx, "", "  ")
}

// Strip returns the API list-view projection of the full index.
//
// Each skill's versions are also stripped to SkillVersionStripped (drops
// body + inputs + requires + entry_point). The caller can further filter
// to a specific channel via (*IndexStripped).FilterByChannel after stripping.
func (idx *Index) Strip() *IndexStripped {
	out := &IndexStripped{
		GeneratedAt: idx.GeneratedAt,
		Schema:      idx.Schema,
		Skills:      make([]SkillStripped, 0, len(idx.Skills)),
	}
	for _, s := range idx.Skills {
		out.Skills = append(out.Skills, s.Strip())
	}
	return out
}

// Strip returns the list-view projection of one skill.
func (s *Skill) Strip() SkillStripped {
	out := SkillStripped{
		Name:        s.Name,
		DisplayName: s.DisplayName,
		Description: s.Description,
		Author:      s.Author,
		Tags:        s.Tags,
		License:     s.License,
		Homepage:    s.Homepage,
		CreatedAt:   s.CreatedAt,
		Category:    s.Category,
		Versions:    make([]SkillVersionStripped, 0, len(s.Versions)),
	}
	for _, v := range s.Versions {
		out.Versions = append(out.Versions, SkillVersionStripped{
			Version:       v.Version,
			Source:        v.Source,
			Channel:       v.Channel,
			ReleasedAt:    v.ReleasedAt,
			Agents:        v.Agents,
			InstallMethod: v.InstallMethod,
			InstallPaths:  v.InstallPaths,
			Zip:           v.Zip,
		})
	}
	return out
}

// FilterByChannel returns a new IndexStripped keeping, for each skill,
// only the latest version in the given channel (semver-max). Skills with
// no matching version are omitted. The input channel may be "latest" as an
// alias for "stable" (npm convention).
//
// This is the server-side projection applied by HandleSkillsList when
// ?channel=X is present, and as the default behavior of the CLI's
// `agentpkg skills list`.
//
// channel="" or channel="stable" with no `latest` alias is treated
// identically (both mean "stable").
func (idx *IndexStripped) FilterByChannel(channel string) *IndexStripped {
	if channel == "" || channel == "latest" {
		channel = "stable"
	}
	out := &IndexStripped{
		GeneratedAt: idx.GeneratedAt,
		Schema:      idx.Schema,
		Skills:      make([]SkillStripped, 0, len(idx.Skills)),
	}
	for _, s := range idx.Skills {
		latest, ok := latestInChannel(s.Versions, channel)
		if !ok {
			continue
		}
		stripped := s
		stripped.Versions = []SkillVersionStripped{latest}
		out.Skills = append(out.Skills, stripped)
	}
	return out
}

// latestInChannel returns the highest-semver version with the matching
// channel. Uses semver.Compare (not string compare) so "1.10.0" > "1.9.0".
//
// If multiple entries tie on semver, the first one wins (deterministic
// given a stable input order — call Sort() beforehand if the input is
// unordered).
func latestInChannel(versions []SkillVersionStripped, channel string) (SkillVersionStripped, bool) {
	var best SkillVersionStripped
	found := false
	for _, v := range versions {
		if v.Channel != channel {
			continue
		}
		if !semver.IsValid("v" + v.Version) {
			continue
		}
		if !found || semver.Compare("v"+v.Version, "v"+best.Version) > 0 {
			best = v
			found = true
		}
	}
	return best, found
}

// SortVersions orders the versions slice in-place by semver descending
// (highest first). Ties keep their input order (stable sort).
func (s *Skill) SortVersions() {
	if len(s.Versions) < 2 {
		return
	}
	indices := make([]int, len(s.Versions))
	for i := range indices {
		indices[i] = i
	}
	sort.SliceStable(indices, func(i, j int) bool {
		vi := "v" + s.Versions[indices[i]].Version
		vj := "v" + s.Versions[indices[j]].Version
		// semver.Compare returns 0 on tie; SliceStable keeps original order
		// for ties via the > 0 check.
		if !semver.IsValid(vi) || !semver.IsValid(vj) {
			return false // leave malformed where it is
		}
		return semver.Compare(vi, vj) > 0
	})
	sorted := make([]SkillVersion, len(s.Versions))
	for i, idx := range indices {
		sorted[i] = s.Versions[idx]
	}
	s.Versions = sorted
}

// FindSkill returns the skill with the given name, or (nil, false).
func (idx *Index) FindSkill(name string) (*Skill, bool) {
	for i := range idx.Skills {
		if idx.Skills[i].Name == name {
			return &idx.Skills[i], true
		}
	}
	return nil, false
}

// FindVersion returns the version entry matching (source, version), or
// (nil, false). Channel is not part of the key (channel is a property of
// the version, not a key dimension — the same source/version cannot exist
// in two channels).
func (s *Skill) FindVersion(source, version string) (*SkillVersion, bool) {
	for i := range s.Versions {
		if s.Versions[i].Source == source && s.Versions[i].Version == version {
			return &s.Versions[i], true
		}
	}
	return nil, false
}

// SemverCompare returns a positive number if a > b, negative if a < b,
// zero if equal. Invalid semver strings fall back to lexicographic
// comparison (so the caller still gets a deterministic ordering rather
// than a panic on malformed state files). Mirrors the comparison used
// internally by the index's LatestInChannel projection.
func SemverCompare(a, b string) int {
	va, vValidA := canonicalize(a)
	vb, vValidB := canonicalize(b)
	if vValidA && vValidB {
		return semver.Compare(va, vb)
	}
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	default:
		return 0
	}
}

// canonicalize prepends "v" (semver requires it) and returns the
// canonical form alongside a validity flag. Returns ("", false) for
// versions that semver.IsValid rejects even with the "v" prefix.
func canonicalize(v string) (string, bool) {
	c := "v" + v
	if !semver.IsValid(c) {
		return "", false
	}
	return c, true
}
