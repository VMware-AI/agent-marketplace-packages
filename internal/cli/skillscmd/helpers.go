package skillscmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
)

// parseSkillZipName is a thin local wrapper around skills.ParseZipFilename,
// kept so the existing CLI tests (TestParseSkillZipName) don't need to
// reach into the skills package directly. Use skills.ParseZipFilename from
// new code.
func parseSkillZipName(filename string) (name, source, version string, err error) {
	return skills.ParseZipFilename(filename)
}

// defaultSkillsDir returns the per-user skills staging directory used by
// `agentpkg skills init`. Resolved lazily so tests can override $HOME
// before calling the command.
func defaultSkillsDir(name string) string {
	if cwd, err := os.Getwd(); err == nil {
		return filepath.Join(cwd, "skills", name)
	}
	return filepath.Join("skills", name)
}

// writeFileAtomic is a tmp + rename helper used by the build / verify
// commands when they need to update dist/skills-index.json without
// leaving a half-written file on disk. Mirrors the pattern used by
// internal/cli/packagecmd/build.go:97-167.
func writeFileAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() {
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
	return os.Rename(tmpName, path)
}

// sha256File streams a file through sha256.New() and returns the hex
// digest. Used by build (sidecar writer) and reindex (size+hash refresh).
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	buf := make([]byte, 32*1024)
	for {
		n, err := f.Read(buf)
		if n > 0 {
			h.Write(buf[:n])
		}
		if err != nil {
			break
		}
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// fileSize returns the file's size in bytes, or 0 on error (consistent
// with how build.go's statSize treats Stat failures).
func fileSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// humanSize formats a byte count as "12.3 MB" / "1.0 KB". Mirrors
// packagecmd.humanSize for consistency in CLI output.
func humanSize(n int64) string {
	const k = 1024
	if n < k {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(k), 0
	for n2 := n / k; n2 >= k; n2 /= k {
		div *= k
		exp++
	}
	units := "KMGTPE"
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), units[exp])
}

// sortedSkillNames returns skill names in alphabetical order. The CLI
// renders skills in deterministic order for readable diffs + test
// stability (HTTP responses may come back in any order).
func sortedSkillNames(idx *skills.Index) []string {
	out := make([]string, 0, len(idx.Skills))
	for _, s := range idx.Skills {
		out = append(out, s.Name)
	}
	sort.Strings(out)
	return out
}
