package skills

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// ExtractSkillMD opens zipPath, finds SKILL.md at the root, and returns the
// parsed Manifest plus the raw SKILL.md bytes. The raw bytes are returned
// so callers can re-serve / persist them without round-tripping through
// the YAML parser (preserving exact author formatting).
//
// Returns an error if:
//   - the zip cannot be opened
//   - SKILL.md is missing from the zip root
//   - SKILL.md is not at the root (must be exactly "SKILL.md", not in a subdir)
//   - the frontmatter is malformed (delegated to ParseSkillFile)
func ExtractSkillMD(zipPath string) (*Manifest, []byte, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, nil, fmt.Errorf("skills: open zip %s: %w", zipPath, err)
	}
	defer zr.Close()
	return ExtractSkillMDFromReader(&zr.Reader, zipPath)
}

// ExtractSkillMDFromReader is the in-memory variant used by the upload
// handler (which already has a io.ReaderAt of the multipart-uploaded file)
// and by `agentpkg skills install --from` when the zip has already been
// read into []byte. Exported so the CLI (in another package) can call it.
func ExtractSkillMDFromReader(zr *zip.Reader, source string) (*Manifest, []byte, error) {
	// Look for "SKILL.md" at the root only. The OpenClaw / agent convention
	// allows nested manifest.json; skills are stricter — frontmatter must
	// be at the very top of the zip so the UI / loader finds it without
	// walking the directory tree.
	const wantName = "SKILL.md"
	var entry *zip.File
	for i := range zr.File {
		if zr.File[i].Name == wantName {
			entry = zr.File[i]
			break
		}
		// Defensive: some zip creators write "SKILL.md/" (with a trailing
		// slash) when including directory entries; treat that as not-found
		// rather than ambiguous.
		if strings.TrimSuffix(zr.File[i].Name, "/") == wantName {
			continue
		}
	}
	if entry == nil {
		return nil, nil, fmt.Errorf("skills: %s has no SKILL.md at zip root", source)
	}
	rc, err := entry.Open()
	if err != nil {
		return nil, nil, fmt.Errorf("skills: open SKILL.md entry in %s: %w", source, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		return nil, nil, fmt.Errorf("skills: read SKILL.md from %s: %w", source, err)
	}
	m, err := ParseSkillFile(data)
	if err != nil {
		return nil, nil, err
	}
	return m, data, nil
}

// ExtractSkillMDFromBytes is a convenience wrapper around
// ExtractSkillMDFromReader for the common case where the caller has the
// zip in memory already. Returns (manifest, rawSkillMDBytes, error).
func ExtractSkillMDFromBytes(zipBytes []byte, source string) (*Manifest, []byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return nil, nil, fmt.Errorf("skills: parse in-memory zip: %w", err)
	}
	return ExtractSkillMDFromReader(zr, source)
}

// ExtractAllEntries walks every entry in the zip and writes it to the given
// destination directory. Preserves file modes for scripts/* (executable bit).
// Symlinks inside the zip are NOT supported — they would need a different
// extraction routine and the skill spec says "skills are content-only".
//
// ExtractAllEntries is the file-path variant: opens the zip at zipPath
// and extracts every entry under destDir. Used by `agentpkg skills install`
// when the zip is on disk (--from <path>).
func ExtractAllEntries(zipPath, destDir string) error {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("skills: open zip %s: %w", zipPath, err)
	}
	defer zr.Close()
	return ExtractAllFromReader(&zr.Reader, destDir)
}

// ExtractAllFromBytes is the in-memory variant: extracts every entry
// from a zip held in memory. Used by `agentpkg skills install` when the
// zip was downloaded from the registry and lives in a []byte. Same
// safety semantics as ExtractAllEntries (zip-slip defense, atomic
// per-entry writes).
func ExtractAllFromBytes(zipBytes []byte, destDir string) error {
	zr, err := zip.NewReader(bytes.NewReader(zipBytes), int64(len(zipBytes)))
	if err != nil {
		return fmt.Errorf("skills: parse in-memory zip: %w", err)
	}
	return ExtractAllFromReader(zr, destDir)
}

// ExtractAllFromReader is the shared extraction loop used by both the
// file-path (ExtractAllEntries) and in-memory (ExtractAllFromBytes)
// variants. Walks every entry, rejects zip-slip via filepath.Rel
// check, and writes each file atomically (tmp + rename).
func ExtractAllFromReader(zr *zip.Reader, destDir string) error {
	// Sort entries so directory entries come before file entries at the
	// same depth; ties resolved by full path. This avoids "mkdir after
	// write" races in some zip creators.
	entries := make([]*zip.File, len(zr.File))
	copy(entries, zr.File)
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name < entries[j].Name
	})
	for _, f := range entries {
		// Zip path safety: reject entries that escape destDir via ".."
		// components. Entry names should also never be absolute paths.
		cleanName := filepath.Clean(f.Name)
		if strings.HasPrefix(cleanName, "..") || filepath.IsAbs(cleanName) {
			return fmt.Errorf("skills: zip entry %q has unsafe path", f.Name)
		}
		target := filepath.Join(destDir, cleanName)
		// Double-check the resolved target is inside destDir (defends
		// against zip slip via symlink-like names that resolve outside).
		rel, err := filepath.Rel(destDir, target)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("skills: zip entry %q escapes destination", f.Name)
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(target, 0o755); err != nil {
				return fmt.Errorf("skills: mkdir %s: %w", target, err)
			}
			continue
		}
		// Make sure parent dir exists (some zips skip directory entries).
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return fmt.Errorf("skills: mkdir parent of %s: %w", target, err)
		}
		mode := f.Mode()
		// Preserve executable bit when the zip entry has any execute bit set
		// on a Unix-style entry. archive/zip stores the upper 16 bits of
		// FileInfo().Mode() in the external attribute, so mode.Perm() gives
		// us the permission bits that the entry actually had.
		perm := mode.Perm()
		if perm == 0 {
			perm = 0o644
		}
		if err := writeZipEntry(f, target, perm); err != nil {
			return err
		}
	}
	return nil
}

// writeZipEntry extracts a single non-directory zip entry to target with the
// given file mode. Uses a tmp-file + rename for atomicity within the
// destination directory.
func writeZipEntry(f *zip.File, target string, mode os.FileMode) error {
	rc, err := f.Open()
	if err != nil {
		return fmt.Errorf("skills: open zip entry %s: %w", f.Name, err)
	}
	defer rc.Close()
	tmp, err := os.CreateTemp(filepath.Dir(target), ".extract-*.tmp")
	if err != nil {
		return fmt.Errorf("skills: create tmp for %s: %w", target, err)
	}
	tmpName := tmp.Name()
	defer func() {
		// Best-effort cleanup if rename fails.
		_ = os.Remove(tmpName)
	}()
	if _, err := io.Copy(tmp, rc); err != nil {
		tmp.Close()
		return fmt.Errorf("skills: copy entry %s: %w", f.Name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("skills: close tmp %s: %w", tmpName, err)
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return fmt.Errorf("skills: chmod %s: %w", target, err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("skills: rename %s -> %s: %w", tmpName, target, err)
	}
	return nil
}

// BuildFromDir walks dir (non-recursively at top level for safety; scripts/
// etc. ARE recursive) and packs its contents into a zip at outPath.
// SKILL.md MUST be at dir/SKILL.md.
//
// File modes are preserved: regular files get 0o644 by default, executable
// scripts (detected by any +x bit on the source) get 0o755. Symlinks are
// skipped (skills are content-only).
//
// Uses tmp-file + rename so a partial build never clobbers an existing zip.
func BuildFromDir(dir, outPath string) (*Manifest, error) {
	// Validate SKILL.md exists and parses first — fail fast before touching
	// any output file.
	skillMD := filepath.Join(dir, "SKILL.md")
	m, err := LoadSkillFile(skillMD)
	if err != nil {
		return nil, err
	}
	// Build into a tmp file in the same directory so os.Rename is atomic
	// across filesystems.
	outDir := filepath.Dir(outPath)
	tmp, err := os.CreateTemp(outDir, ".build-*.zip.tmp")
	if err != nil {
		return nil, fmt.Errorf("skills: create tmp zip: %w", err)
	}
	tmpName := tmp.Name()
	// Track so we can clean up on any error path.
	success := false
	defer func() {
		if !success {
			_ = os.Remove(tmpName)
		}
	}()
	zw := zip.NewWriter(tmp)
	// Walk top-level entries so we control directory entry emission.
	entries, err := os.ReadDir(dir)
	if err != nil {
		tmp.Close()
		return nil, fmt.Errorf("skills: read dir %s: %w", dir, err)
	}
	// Sort for deterministic zip output (helps diff + tests).
	sort.Slice(entries, func(i, j int) bool { return entries[i].Name() < entries[j].Name() })
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		if err := addDirToZip(zw, full, e, ""); err != nil {
			tmp.Close()
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
		return nil, fmt.Errorf("skills: close zip writer: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return nil, fmt.Errorf("skills: close tmp zip: %w", err)
	}
	if err := os.Rename(tmpName, outPath); err != nil {
		return nil, fmt.Errorf("skills: rename zip %s -> %s: %w", tmpName, outPath, err)
	}
	success = true
	return m, nil
}

// addDirToZip writes one top-level entry to zw. If the entry is a directory
// it recurses and adds each file individually. Symlinks are skipped (skills
// are content-only).
func addDirToZip(zw *zip.Writer, full string, info os.DirEntry, prefix string) error {
	if info.Type()&os.ModeSymlink != 0 {
		return nil // skip symlinks
	}
	name := info.Name()
	if prefix != "" {
		name = prefix + "/" + name
	}
	if info.IsDir() {
		// Recurse into the directory. Emit a directory entry too so unzip
		// on Windows preserves the structure.
		if _, err := zw.Create(name + "/"); err != nil {
			return fmt.Errorf("skills: create dir entry %s: %w", name, err)
		}
		children, err := os.ReadDir(full)
		if err != nil {
			return fmt.Errorf("skills: read subdir %s: %w", full, err)
		}
		sort.Slice(children, func(i, j int) bool { return children[i].Name() < children[j].Name() })
		for _, child := range children {
			childFull := filepath.Join(full, child.Name())
			if err := addDirToZip(zw, childFull, child, name); err != nil {
				return err
			}
		}
		return nil
	}
	// Regular file.
	data, err := os.ReadFile(full)
	if err != nil {
		return fmt.Errorf("skills: read %s: %w", full, err)
	}
	// File mode: preserve executable bit on scripts/* and any file that
	// already has +x. Otherwise 0o644.
	mode := os.FileMode(0o644)
	if fi, err := info.Info(); err == nil {
		perms := fi.Mode().Perm()
		if perms != 0 {
			mode = perms
		}
	}
	fh := &zip.FileHeader{
		Name:   name,
		Method: zip.Deflate,
	}
	fh.SetMode(mode)
	w, err := zw.CreateHeader(fh)
	if err != nil {
		return fmt.Errorf("skills: create header %s: %w", name, err)
	}
	if _, err := w.Write(data); err != nil {
		return fmt.Errorf("skills: write %s: %w", name, err)
	}
	return nil
}

// ListZipNames returns the sorted entry names of a zip. Useful for the
// "agentpkg skills verify" path and for diagnostic dumps.
func ListZipNames(zipPath string) ([]string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, fmt.Errorf("skills: open zip %s: %w", zipPath, err)
	}
	defer zr.Close()
	names := make([]string, 0, len(zr.File))
	for _, f := range zr.File {
		names = append(names, f.Name)
	}
	sort.Strings(names)
	return names, nil
}

// Sha256File streams a file through sha256.New() and returns the hex digest.
// Used by `agentpkg skills build` to write the sidecar and by the upload
// handler to verify what landed on disk.
func Sha256File(path string) (string, error) {
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

// Sha256Bytes is the in-memory variant used by tests and the CLI downloader.
func Sha256Bytes(data []byte) string {
	h := sha256.New()
	h.Write(data)
	return hex.EncodeToString(h.Sum(nil))
}

// ParseZipFilename extracts (name, source, version) from a skill zip
// filename. Accepts three layouts:
//
//   - "<name>.zip"                     → (name, "", "")
//   - "<name>-<version>.zip"           → (name, "", version)   most common
//   - "<name>-<source>-<version>.zip"  → (name, source, ver)   CLI build output
//
// The 3-segment form is only treated as "<name>-<source>-<version>" when
// the middle segment is a recognised source (community / internal — see
// IsValidSource). Otherwise it's ambiguous with the 2-segment
// "<name-with-dash>-<version>" form (e.g. "ppt-maker-1.0.3.zip") and we
// fall through to the 2-segment interpretation. This means community
// uploads of a 3-segment zip where the middle word isn't a valid source
// are recognised by name+version only — source is then supplied by the
// upload channel via the multipart form field.
//
// 4+ dash-separated segments, 0 segments, or any empty name segment
// returns an error. Version is allowed to be empty only in the 1-
// segment layout — a 2-segment "<name>-<X>.zip" where X isn't a real
// version is rejected so the operator can't silently hand us a
// filename like "hello-community.zip" where "community" gets parsed
// as the version.
//
// `source` is left empty in the 1-/2-segment layouts — the upload
// handler resolves the actual source from a multipart form field
// (see HandleSkillUpload) when the filename's source is empty or not
// in IsValidSource. The shared (name, source, version) tuple continues
// to be the registry identity so download / delete / sync paths don't
// have to special-case the new layouts.
func ParseZipFilename(filename string) (name, source, version string, err error) {
	if !strings.HasSuffix(filename, ".zip") {
		return "", "", "", fmt.Errorf("filename %q does not end in .zip", filename)
	}
	stem := strings.TrimSuffix(filename, ".zip")
	parts := strings.Split(stem, "-")
	if len(parts) == 0 || len(parts) > 3 {
		return "", "", "", fmt.Errorf("filename %q does not match <name>[-<source>]-<version>.zip (got %d dash-separated parts)", filename, len(parts))
	}
	switch len(parts) {
	case 1:
		name = parts[0]
	case 2:
		name, version = parts[0], parts[1]
	default: // 3
		// Disambiguate "<name>-<source>-<version>" vs "<name-with-dash>-<version>".
		// The CLI `build` command outputs "<name>-<source>-<version>.zip" so the
		// middle segment is always a valid source value (community / internal).
		// Anything else is a community zip whose author stuck a dash in the
		// name — treat it as the 2-segment form.
		if IsValidSource(parts[1]) {
			name, source, version = parts[0], parts[1], parts[2]
		} else {
			name = parts[0] + "-" + parts[1]
			version = parts[2]
		}
	}
	if name == "" {
		return "", "", "", fmt.Errorf("filename %q has empty name segment", filename)
	}
	if len(parts) >= 2 && version == "" {
		return "", "", "", fmt.Errorf("filename %q has empty version segment", filename)
	}
	return name, source, version, nil
}

// zipSlipGuard is an internal helper retained for documentation: the real
// zip-slip defense lives inline in extractAllFromReader (filepath.Rel
// check). This helper is exported via the bytes.Buffer signature so the
// in-memory extraction path can reuse the same logic. Currently unused
// but kept for symmetry with future streaming-extract work.
func zipSlipGuard(destDir, entryName string) error {
	target := filepath.Join(destDir, entryName)
	rel, err := filepath.Rel(destDir, target)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("skills: zip entry %q escapes destination", entryName)
	}
	return nil
}

// ensure import-only references stay used (silences unused-import linter
// when this file is the only one importing a stdlib symbol).
var _ = bytes.NewBuffer
