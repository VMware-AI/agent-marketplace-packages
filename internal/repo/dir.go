// Package repo provides read access to the dist/ directory.
//
// In the marketplace-api server, repo is the bridge between the on-disk
// dist/ layout (tarballs + sha256 + index.json) and the in-memory apitypes.Index.
// In agentpkg (client), repo provides the same loaders for index.json used by
// `package build` and `package reindex`.
//
// The same Dir also serves the parallel skills layout (dist/skills/ +
// dist/skills-index.json) via the SkillsPath / LoadSkillsIndex /
// ValidateSkills / ListSkillZips methods. The two layouts are kept
// completely separate — agents and skills never share files.
package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"gopkg.in/yaml.v3"
)

// Dir is a read-only view of a dist/ directory.
type Dir struct {
	// Path is the absolute path to the agent tarballs directory
	// (mounted read-only in container deployments — the package source
	// that the operator pushes to). Skill storage lives elsewhere
	// (see SkillsRoot) so skill uploads don't require a writable
	// mount of the agent tarballs source.
	Path string
	// SkillsRoot is the absolute path to the writable skills storage
	// directory. Holds skill zips, their .sha256 sidecars, and
	// skills-index.json. Distinct from Path because agent tarballs
	// are immutable / read-only in production deployments while
	// skills are uploaded at runtime and must be writable.
	//
	// When constructed via NewDir, SkillsRoot defaults to
	// `<Path>/skills` for backward compatibility. Use NewDirEx to
	// point at a separate physical mount (the recommended deployment
	// shape).
	SkillsRoot string
}

// NewDir returns a Dir rooted at path. SkillsRoot defaults to
// `<path>/skills` — the historical layout. Use NewDirEx to override.
func NewDir(path string) *Dir {
	return &Dir{Path: path, SkillsRoot: filepath.Join(path, skillsDirName)}
}

// NewDirEx returns a Dir with the agent tarballs source rooted at
// distPath and the skills storage rooted at skillsRoot. The two
// directories MUST be on different filesystems (or at least one
// writable and the other read-only) so skill uploads can write
// without breaking agent-tarball immutability.
//
// When skillsRoot is empty, falls back to the NewDir default
// (`<distPath>/skills`) so callers don't accidentally produce a
// misconfigured Dir.
func NewDirEx(distPath, skillsRoot string) *Dir {
	if skillsRoot == "" {
		skillsRoot = filepath.Join(distPath, skillsDirName)
	}
	return &Dir{Path: distPath, SkillsRoot: skillsRoot}
}

// TarballPath returns the absolute path of a given tarball inside the dir.
func (d *Dir) TarballPath(filename string) string {
	return filepath.Join(d.Path, filename)
}

// SHA256FilePath returns the absolute path of the .sha256 sidecar file for
// a given tarball.
func (d *Dir) SHA256FilePath(tarballName string) string {
	return d.TarballPath(tarballName + ".sha256")
}

// ListTarballs returns all *.tar.gz filenames in the directory, sorted.
func (d *Dir) ListTarballs() ([]string, error) {
	entries, err := os.ReadDir(d.Path)
	if err != nil {
		return nil, fmt.Errorf("read dist/ dir: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".tar.gz") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// ReadSHA256 reads a .sha256 sidecar file. The file format is the standard
// `sha256sum` format: "<hex>  <filename>".
func (d *Dir) ReadSHA256(tarballName string) (string, error) {
	data, err := os.ReadFile(d.SHA256FilePath(tarballName))
	if err != nil {
		return "", fmt.Errorf("read sha256 sidecar: %w", err)
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		return "", fmt.Errorf("sha256 sidecar malformed: %q", string(data))
	}
	return fields[0], nil
}

// StatTarball returns the size in bytes of a tarball.
func (d *Dir) StatTarball(tarballName string) (int64, error) {
	fi, err := os.Stat(d.TarballPath(tarballName))
	if err != nil {
		return 0, fmt.Errorf("stat tarball: %w", err)
	}
	return fi.Size(), nil
}

// LoadIndex reads dist/index.json.
func (d *Dir) LoadIndex() (*apitypes.Index, error) {
	data, err := os.ReadFile(filepath.Join(d.Path, "index.json"))
	if err != nil {
		return nil, fmt.Errorf("read index.json: %w", err)
	}
	return apitypes.ParseIndex(data)
}

// Validate performs integrity checks on the dist/ directory: every tarball
// referenced in index.json must exist and its sha256 must match the sidecar
// .sha256 file. Used at marketplace-api startup.
func (d *Dir) Validate(idx *apitypes.Index) error {
	for _, agent := range idx.Agents {
		for _, v := range agent.Versions {
			tarball := v.Tarball.Filename
			tarPath := d.TarballPath(tarball)
			if _, err := os.Stat(tarPath); err != nil {
				return fmt.Errorf("missing tarball %s for %s/%s/%s: %w",
					tarball, agent.Name, v.Source, v.Version, err)
			}
			expectedHex := strings.TrimPrefix(v.Tarball.SHA256, "sha256:")
			actual, err := d.ReadSHA256(tarball)
			if err != nil {
				return fmt.Errorf("read sha256 for %s: %w", tarball, err)
			}
			if actual != expectedHex {
				return fmt.Errorf("sha256 mismatch for %s: expected %s, got %s",
					tarball, expectedHex, actual)
			}
		}
	}
	return nil
}

// ParseTarball parses a tarball into manifest + meta by extracting the
// top-level meta.yaml and manifest.json files. Used by client (agentpkg
// package build / reindex) when constructing index.json from tarballs.
//
// Implementation note: this is the ONLY place where we extract files from
// a tarball — marketplace-api runtime never extracts (see plan §index.json).
func ParseTarball(tarballPath string) (*manifest.Manifest, *manifest.Meta, error) {
	data, err := os.ReadFile(tarballPath)
	if err != nil {
		return nil, nil, fmt.Errorf("read tarball: %w", err)
	}
	manifestData, metaData, err := ExtractManifestAndMeta(data)
	if err != nil {
		return nil, nil, err
	}
	m, err := manifest.LoadFromBytes(manifestData)
	if err != nil {
		return nil, nil, fmt.Errorf("parse manifest.json: %w", err)
	}
	// meta.yaml is optional in the tarball — only present if author
	// included it (post-refactor layout).
	if metaData == nil {
		return m, nil, nil
	}
	meta := &manifest.Meta{}
	if err := yaml.Unmarshal(metaData, meta); err != nil {
		return nil, nil, fmt.Errorf("parse meta.yaml: %w", err)
	}
	return m, meta, nil
}

// FileSHA256 returns the sha256 hex digest of a single file.
func FileSHA256(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// -----------------------------------------------------------------------
// Skills layout (<SkillsRoot>/ + <SkillsRoot>/skills-index.json).
//
// The skills subsystem is independent of the agent subsystem — it has its
// own directory (SkillsRoot), its own index file (skills-index.json
// alongside the zips), its own sidecar format (.zip.sha256 — same
// sha256sum format as the agent tarballs). The methods below mirror the
// agent-facing ones above.
//
// SkillsRoot is intentionally a separate filesystem location from Path
// (the agent tarballs source) so skill uploads can write to a writable
// mount without making the agent tarballs directory mutable.
// -----------------------------------------------------------------------

// skillsDirName is retained as a const so the historical default
// layout (`<distPath>/skills`) keeps working for callers using
// NewDir. NewDirEx decouples the two roots; new deployments should
// prefer that constructor.
const skillsDirName = "skills"

// skillsIndexName is the on-disk filename for the skills index, parallel
// to index.json for agents.
const skillsIndexName = "skills-index.json"

// SkillsPath returns the absolute path to the skills directory
// (the directory holding skill zips).
func (d *Dir) SkillsPath() string {
	return d.SkillsRoot
}

// SkillsIndexPath returns the absolute path to skills-index.json
// (sibling of the zips under SkillsRoot).
func (d *Dir) SkillsIndexPath() string {
	return filepath.Join(d.SkillsRoot, skillsIndexName)
}

// SkillZipPath returns the absolute path of a skill zip inside dist/skills/.
func (d *Dir) SkillZipPath(filename string) string {
	return filepath.Join(d.SkillsPath(), filename)
}

// SkillSHA256Path returns the absolute path of a .sha256 sidecar for a
// given skill zip (filename + ".sha256", stored next to the zip).
func (d *Dir) SkillSHA256Path(filename string) string {
	return d.SkillZipPath(filename + ".sha256")
}

// ListSkillZips returns all *.zip filenames under dist/skills/, sorted.
// Returns an empty slice (not an error) if dist/skills/ does not yet exist —
// that's the normal "no skills uploaded yet" state.
func (d *Dir) ListSkillZips() ([]string, error) {
	entries, err := os.ReadDir(d.SkillsPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read dist/skills/: %w", err)
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasSuffix(e.Name(), ".zip") {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}

// LoadSkillsIndex reads dist/skills-index.json. Returns an empty
// *skills.Index (not an error) when the file does not yet exist — that's
// the normal startup state for a fresh dist/skills/. Other IO or parse
// errors are returned as-is.
//
// Caller MUST treat the returned *skills.Index as immutable once published
// via State.SetSkillsIndex — see the same contract on State.Index().
func (d *Dir) LoadSkillsIndex() (*skills.Index, error) {
	data, err := os.ReadFile(d.SkillsIndexPath())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			// Empty index — schema_version matches IndexSchemaVersion.
			return &skills.Index{Schema: skills.IndexSchemaVersion}, nil
		}
		return nil, fmt.Errorf("read %s: %w", skillsIndexName, err)
	}
	return skills.ParseIndexBytes(data)
}

// ValidateSkills performs integrity checks on dist/skills/ + dist/skills-index.json:
// every skill version referenced in the index must have a corresponding
// .zip on disk AND a .zip.sha256 sidecar whose hex matches the index entry.
// Mirrors Dir.Validate() for agents; called at marketplace-api startup.
func (d *Dir) ValidateSkills(idx *skills.Index) error {
	for _, s := range idx.Skills {
		for _, v := range s.Versions {
			filename := v.Zip.Filename
			zipPath := d.SkillZipPath(filename)
			if _, err := os.Stat(zipPath); err != nil {
				return fmt.Errorf("missing skill zip %s for %s/%s/%s: %w",
					filename, s.Name, v.Source, v.Version, err)
			}
			expectedHex := strings.TrimPrefix(v.Zip.SHA256, "sha256:")
			sidecarPath := d.SkillSHA256Path(filename)
			data, err := os.ReadFile(sidecarPath)
			if err != nil {
				return fmt.Errorf("read sha256 sidecar for %s: %w", filename, err)
			}
			fields := strings.Fields(string(data))
			if len(fields) < 1 {
				return fmt.Errorf("sha256 sidecar %s malformed", sidecarPath)
			}
			if fields[0] != expectedHex {
				return fmt.Errorf("sha256 mismatch for %s: expected %s, got %s",
					filename, expectedHex, fields[0])
			}
		}
	}
	return nil
}
