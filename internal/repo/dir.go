// Package repo provides read access to the dist/ directory.
//
// In the marketplace-api server, repo is the bridge between the on-disk
// dist/ layout (tarballs + sha256 + index.json) and the in-memory apitypes.Index.
// In agentpkg (client), repo provides the same loaders for index.json used by
// `package build` and `package reindex`.
package repo

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"gopkg.in/yaml.v3"
)

// Dir is a read-only view of a dist/ directory.
type Dir struct {
	Path string // absolute path to dist/
}

// NewDir returns a Dir rooted at path.
func NewDir(path string) *Dir { return &Dir{Path: path} }

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
