package skillscmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// SkillInstallState is the per-skill local state written to
// <target-dir>/<name>/state.json by `agentpkg skills install`.
//
// Mirrors the layout documented in the Phase 8 plan §1.6. Two top-level
// fields make the common cases easy to read:
//
//   - CurrentVersion / Source / Channel: the version that's currently
//     active (i.e. the target of the `latest` symlink). New installs of
//     a different version flip these; old versions stay in
//     InstalledVersions until uninstall removes them.
//
//   - InstalledVersions: keyed by version string. Multi-version installs
//     (e.g. 1.0.0 stable + 2.0.0-beta.1 beta) coexist; only one is
//     "current".
type SkillInstallState struct {
	Name           string `json:"name"`
	CurrentVersion string `json:"current_version,omitempty"`
	Source         string `json:"source,omitempty"`
	Channel        string `json:"channel,omitempty"`
	TargetDir      string `json:"target_dir,omitempty"`

	// === Schema v2.0 (optional in state.json — old files without these
	// are still parseable; the install layer treats empty as defaults) ===
	//
	// InstallMethod is the pipeline used at install time. Empty is
	// normalized to "zip-extract" at read for backward compat.
	InstallMethod string `json:"install_method,omitempty"`
	// Agents is the set of runtimes this skill was installed for.
	// Empty is treated as ["all"] (the historical single-target install).
	Agents []string `json:"agents,omitempty"`
	// ResolvedPaths records the actual install location per agent.
	// Keys are agent IDs ("all" for the central fallback). The value
	// is the fully-expanded on-disk path (already $HOME/$NAME-substituted).
	ResolvedPaths map[string]string `json:"resolved_paths,omitempty"`

	InstalledVersions map[string]InstalledVersion `json:"installed_versions"`
}

// InstalledVersion is one entry in SkillInstallState.InstalledVersions.
//
// ZipFilename + ZipSHA256 are recorded so uninstall + re-install can be
// idempotent: re-installing the same (source, version) reuses the
// recorded metadata rather than re-downloading.
type InstalledVersion struct {
	Source      string `json:"source"`
	Channel     string `json:"channel"`
	InstalledAt string `json:"installed_at"`
	ZipSHA256   string `json:"zip_sha256,omitempty"`
	ZipFilename string `json:"zip_filename,omitempty"`
	ReleasedAt  string `json:"released_at,omitempty"`
}

// skillStatePath returns the path to <target-dir>/<name>/state.json.
// Exported via the package boundary so install / uninstall / list-installed
// can share the layout without re-implementing it.
func skillStatePath(targetDir, name string) string {
	return filepath.Join(targetDir, name, "state.json")
}

// skillInstallDir returns <target-dir>/<name>/<version>/ — the per-version
// staging directory the zip extracts into.
func skillInstallDir(targetDir, name, version string) string {
	return filepath.Join(targetDir, name, version)
}

// skillLatestSymlink returns the path of the `latest` symlink inside the
// skill directory.
func skillLatestSymlink(targetDir, name string) string {
	return filepath.Join(targetDir, name, "latest")
}

// loadSkillState reads <target-dir>/<name>/state.json. Returns
// (nil, nil) for a clean "not installed" — callers can treat that as
// a fresh install. Returns an error only for I/O / parse failures.
func loadSkillState(targetDir, name string) (*SkillInstallState, error) {
	p := skillStatePath(targetDir, name)
	data, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("read %s: %w", p, err)
	}
	var st SkillInstallState
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse %s: %w", p, err)
	}
	if st.InstalledVersions == nil {
		st.InstalledVersions = map[string]InstalledVersion{}
	}
	return &st, nil
}

// saveSkillState writes the state via writeFileAtomic so a crash mid-write
// doesn't leave a half-written state.json behind. The "no versions
// installed" case writes an empty-state JSON file rather than deleting
// it — that preserves mtimes for debugging and matches user expectations
// that "list-installed" still shows the skill as uninstalled, not missing.
func saveSkillState(targetDir string, st *SkillInstallState) error {
	if st.InstalledVersions == nil {
		st.InstalledVersions = map[string]InstalledVersion{}
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}
	p := skillStatePath(targetDir, st.Name)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", filepath.Dir(p), err)
	}
	if err := writeFileAtomic(p, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", p, err)
	}
	return nil
}

// removeSkillState deletes <target-dir>/<name>/state.json. Best-effort:
// a missing file is not an error (the caller is removing a not-installed
// skill).
func removeSkillState(targetDir, name string) error {
	p := skillStatePath(targetDir, name)
	if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// updateLatestSymlink points <target-dir>/<name>/latest at
// <target-dir>/<name>/<version>/. Existing symlinks are replaced via
// os.Remove + os.Symlink (os.Symlink refuses to overwrite).
//
// Returns an error if the symlink target directory doesn't exist —
// callers should ensure skillInstallDir(targetDir, name, version) has
// been created first.
func updateLatestSymlink(targetDir, name, version string) error {
	link := skillLatestSymlink(targetDir, name)
	// Best-effort remove of any existing symlink. Missing file is fine.
	_ = os.Remove(link)
	target := version
	if err := os.Symlink(target, link); err != nil {
		return fmt.Errorf("symlink %s -> %s: %w", link, target, err)
	}
	return nil
}

// clearLatestSymlink removes the `latest` symlink (if present). Used
// when uninstalling the currently-active version with no replacement.
func clearLatestSymlink(targetDir, name string) {
	_ = os.Remove(skillLatestSymlink(targetDir, name))
}

// nowUTC is defined in build.go to avoid duplication across files
// (time.Now().UTC().Format(time.RFC3339) is repeated in several places).

// installedVersionsSorted returns the version keys of InstalledVersions
// in semver-descending order. Stable sort so the output is deterministic
// for tests + CLI display. Falls back to lexicographic order if any
// version fails semver.IsValid (defensive against malformed state files).
func installedVersionsSorted(st *SkillInstallState) []string {
	out := make([]string, 0, len(st.InstalledVersions))
	for v := range st.InstalledVersions {
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		return skills.SemverCompare(out[i], out[j]) > 0
	})
	return out
}

// resolveInstalledSkill looks up <target-dir>/<name>/ and returns its
// state, or (nil, nil) if not installed. Convenience wrapper around
// loadSkillState that callers in install/uninstall/list-installed share.
func resolveInstalledSkill(targetDir, name string) (*SkillInstallState, error) {
	return loadSkillState(targetDir, name)
}

// EffectiveInstallMethod returns st.InstallMethod normalized to a
// known value. Empty (legacy state) is treated as "zip-extract" so old
// state files keep working with the v2.0 dispatcher.
func (st *SkillInstallState) EffectiveInstallMethod() string {
	if st == nil || st.InstallMethod == "" {
		return "zip-extract"
	}
	return st.InstallMethod
}

// EffectiveAgents returns st.Agents normalized. Empty (legacy) is
// treated as ["all"] — the central fallback.
func (st *SkillInstallState) EffectiveAgents() []string {
	if st == nil || len(st.Agents) == 0 {
		return []string{"all"}
	}
	return st.Agents
}