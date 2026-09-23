package server

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/VMware-AI/agent-marketplace-packages/internal/repo"
)

// SkillsDir wraps repo.Dir with a write lock for serializing skill upload,
// delete, and reload operations. Reads do NOT take the lock — they go
// through atomic.Pointer[skills.Index] for race-free concurrent access.
//
// The mutex is process-wide per SkillsDir instance (i.e. one lock per
// marketplace-api process); chi does NOT serialize handlers, so without
// this we'd have two concurrent uploads racing on the same skills-index.json
// write — losing one of the resulting index files on the way out.
type SkillsDir struct {
	dist *repo.Dir
	mu   sync.Mutex
}

// NewSkillsDir constructs a SkillsDir wrapping the given repo.Dir (the
// dist/ root).
func NewSkillsDir(d *repo.Dir) *SkillsDir {
	return &SkillsDir{dist: d}
}

// Lock acquires the write lock. Held by upload + delete handlers + any
// reload path that mutates dist/skills/. Returned so callers can defer
// Unlock; the pattern is `sd.Lock(); defer sd.Unlock()`.
func (sd *SkillsDir) Lock() { sd.mu.Lock() }

// Unlock releases the write lock.
func (sd *SkillsDir) Unlock() { sd.mu.Unlock() }

// TmpDir returns the path used for transient .tmp/ files during upload
// and reindex. Auto-created on first use.
func (sd *SkillsDir) TmpDir() string {
	return filepath.Join(sd.dist.SkillsPath(), ".tmp")
}

// SkillsPath returns the on-disk path to dist/skills/.
func (sd *SkillsDir) SkillsPath() string { return sd.dist.SkillsPath() }

// ZipPath returns the absolute path to a skill zip inside dist/skills/.
func (sd *SkillsDir) ZipPath(filename string) string {
	return sd.dist.SkillZipPath(filename)
}

// SHA256Path returns the absolute path to a skill zip's .sha256 sidecar.
func (sd *SkillsDir) SHA256Path(filename string) string {
	return sd.dist.SkillSHA256Path(filename)
}

// IndexPath returns the absolute path to dist/skills-index.json.
func (sd *SkillsDir) IndexPath() string { return sd.dist.SkillsIndexPath() }

// EnsureTmpDir creates the .tmp/ subdir if it doesn't already exist.
// Returns the path (same as TmpDir()). Idempotent.
func (sd *SkillsDir) EnsureTmpDir() (string, error) {
	p := sd.TmpDir()
	if err := os.MkdirAll(p, 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// RemoveZip removes a skill zip + its .sha256 sidecar. Missing files are
// silently ignored (idempotent semantics: delete twice = delete once).
func (sd *SkillsDir) RemoveZip(filename string) {
	_ = os.Remove(sd.ZipPath(filename))
	_ = os.Remove(sd.SHA256Path(filename))
}

// IsValidSource reports whether s is an allowed source value for a URL
// path parameter. Rejects path traversal attempts ("..", ".", etc.) and
// unknown sources before any handler touches the index.
func IsValidSource(s string) bool {
	if s == "" || strings.ContainsAny(s, "/.\\") {
		return false
	}
	return validSkillSource[s]
}

// validSkillSource is the closed set of allowed source path values.
// Kept here (not in internal/skills) because it's an HTTP-API concern:
// which values are acceptable as URL path components.
var validSkillSource = map[string]bool{
	"community": true,
	"internal":  true,
}

// safeOpen is a small helper that opens a file and returns an error
// distinguishable from "missing" (os.ErrNotExist).
func safeOpen(path string) (*os.File, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return f, nil
}

// writeFileAtomic is the tmp + rename + fsync pattern reused by the
// upload handler + the skills CLI build command. Writes data to path.tmp
// in the same directory, fsyncs, renames into place. Keeps the same
// pattern used by internal/cli/packagecmd/build.go for tarballs.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename fails. Without this, a failed
		// rename leaves a .tmp-* in dist/skills/ that pollutes later
		// ListSkillZips() unless filtered out.
		_ = os.Remove(tmpName)
	}()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		return err
	}
	return nil
}

// discardReadCloser wraps an io.Reader so it satisfies io.ReadCloser
// (Close is a no-op). Used when streaming an os.File to io.Copy without
// pulling in extra dependencies.
type discardReadCloser struct {
	io.Reader
	closed bool
}

func (d *discardReadCloser) Close() error {
	d.closed = true
	return nil
}

// Ensure io.ReadCloser is referenced for go vet.
var _ io.ReadCloser = (*discardReadCloser)(nil)

// errorsIsNotExist is a tiny helper to avoid scattering errors.Is checks.
// Kept here so callers can write `if errorsIsNotExist(err) { ... }`.
func errorsIsNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
