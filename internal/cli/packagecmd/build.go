package packagecmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/VMware-AI/agent-marketplace-packages/internal/manifest"
	"github.com/spf13/cobra"
)

// NewBuildCmd creates `agentpkg package build`.
//
// Builds a single tar.gz from agents/<name>/<source>/<version>/, embeds
// the matching meta.yaml at the tarball root, writes dist/<name>-<source>-<version>.tar.gz
// + .sha256, and regenerates dist/index.json by scanning every tarball.
func NewBuildCmd() *cobra.Command {
	var (
		version string
		source  string
		channel string
		outDir  string
		dryRun  bool
	)
	c := &cobra.Command{
		Use:   "build <path> --version X.Y.Z [--source upstream] [--channel stable] [--out <path>/dist]",
		Short: "Package one version of an agent into <outDir>/<name>-<source>-<version>.tar.gz + .sha256 and refresh index.json",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if version == "" {
				return fmt.Errorf("--version is required")
			}
			if source == "" {
				source = "upstream"
			}
			if channel == "" {
				channel = "stable"
			}
			// F022 / F025: buildOne resolves the empty --out
			// default to <agent-dir>/dist and reads the tarball
			// name from manifest.agent (not filepath.Base(dir)).
			// We just pass the args through; see buildOne's
			// comments for the rationale.
			return buildOne(args[0], source, channel, version, outDir, dryRun)
		},
	}
	c.Flags().StringVar(&version, "version", "", "semver version segment (required)")
	c.Flags().StringVar(&source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | dev | internal)")
	// Empty default → resolve to <agent-dir>/dist in RunE. See F022.
	c.Flags().StringVar(&outDir, "out", "", "output directory (default: <agent-dir>/dist; explicit value is CWD-relative)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "build the tarball in a temp dir but don't write dist/")
	return c
}

// NewReindexCmd creates `agentpkg package reindex`.
func NewReindexCmd() *cobra.Command {
	var (
		outDir string
		dryRun bool
	)
	c := &cobra.Command{
		Use:   "reindex [--out dist]",
		Short: "Rescan every tarball in dist/ and rewrite dist/index.json",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if outDir == "" {
				outDir = "dist"
			}
			return reindexAll(outDir, dryRun)
		},
	}
	c.Flags().StringVar(&outDir, "out", "dist", "output directory")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "don't write index.json (only print)")
	return c
}

// buildOne packages agents/<name>/<source>/<version>/ into
// dist/<name>-<source>-<version>.tar.gz.
func buildOne(agentDir, source, channel, version, outDir string, dryRun bool) error {
	payloadDir := filepath.Join(agentDir, source, version)
	mf := filepath.Join(payloadDir, "manifest.json")
	if _, err := os.Stat(mf); err != nil {
		return fmt.Errorf("missing manifest.json at %s", mf)
	}

	// F022 (tested 2026-09-30): resolve an empty outDir to
	// ``<agentDir>/dist`` so the build lands next to the source
	// directory regardless of CWD. The cobra command's RunE passes
	// through the empty default; the resolution happens here so
	// callers (and tests) that bypass the CLI get consistent
	// behavior. The CLI's --out flag is also "" by default for the
	// same reason — see NewBuildCmd's flag binding.
	if outDir == "" {
		outDir = filepath.Join(agentDir, "dist")
	}

	// F025 (tested 2026-09-30): the previous code derived the
	// tarball name from ``filepath.Base(agentDir)`` — so
	// ``package build /tmp/pkg-init`` (where agentDir was scaffolded
	// with ``package init my-test-agent --dir /tmp/pkg-init``) wrote
	// ``pkg-init-upstream-0.1.0.tar.gz`` even though the agent's
	// canonical identity (per ``manifest.json``) is ``my-test-agent``.
	// The dist/index.json entry used the manifest's agent field but
	// the on-disk filename did not, so ``download <tarball>`` lookups
	// by name failed. We now read the agent identity from
	// ``manifest.agent`` so the filename matches the index entry and
	// the rest of the registry.
	m, err := manifest.Load(mf)
	if err != nil {
		return fmt.Errorf("load %s: %w", mf, err)
	}
	if m.Agent == "" {
		return fmt.Errorf("%s has empty agent field — cannot derive tarball name", mf)
	}
	name := m.Agent

	// Step 1: verify first (fast fail).
	if err := verifyAgent(agentDir, true); err != nil {
		return fmt.Errorf("verification failed: %w", err)
	}

	if err := os.MkdirAll(outDir, 0755); err != nil {
		return fmt.Errorf("mkdir %s: %w", outDir, err)
	}

	// Step 2: create tarball in a temp dir, then atomically move.
	tmpTar, err := os.CreateTemp("", "agentpkg-build-*.tar.gz")
	if err != nil {
		return err
	}
	defer os.Remove(tmpTar.Name())
	gz := gzip.NewWriter(tmpTar)
	tw := tar.NewWriter(gz)
	if err := addFileToTar(tw, mf, "manifest.json"); err != nil {
		return err
	}
	// Embed meta.yaml at the tarball root, if present.
	metaPath := filepath.Join(agentDir, "meta.yaml")
	if _, err := os.Stat(metaPath); err == nil {
		if err := addFileToTar(tw, metaPath, "meta.yaml"); err != nil {
			return err
		}
	}
	// Walk payload/, runtime/, migrate/, README.md, etc.
	if err := addDirToTar(tw, payloadDir, ""); err != nil {
		return err
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	if err := tmpTar.Close(); err != nil {
		return err
	}

	tarName := fmt.Sprintf("%s-%s-%s.tar.gz", name, source, version)
	finalPath := filepath.Join(outDir, tarName)
	if dryRun {
		fmt.Printf("(dry-run) would write %s (%d bytes)\n", finalPath, statSize(tmpTar.Name()))
		return nil
	}
	if err := os.Rename(tmpTar.Name(), finalPath); err != nil {
		return fmt.Errorf("rename: %w", err)
	}

	// Step 3: compute sha256 + write .sha256 sidecar atomically. We
	// previously wrote the sidecar directly to its final path, which left
	// a window during which the tarball existed with a missing or
	// half-written sidecar — and a running marketplace-api process doing
	// a re-read would fail Validate. temp + rename matches the pattern
	// already used for the tarball (step 2) and index.json (step 4).
	sha, err := computeFileSHA256(finalPath)
	if err != nil {
		return err
	}
	shaPath := finalPath + ".sha256"
	tmpSha, err := os.CreateTemp(outDir, "agentpkg-sha-*.tmp")
	if err != nil {
		return fmt.Errorf("create tmp sha file: %w", err)
	}
	if _, err := tmpSha.Write([]byte(sha + "  " + tarName + "\n")); err != nil {
		tmpSha.Close()
		os.Remove(tmpSha.Name())
		return fmt.Errorf("write tmp sha: %w", err)
	}
	if err := tmpSha.Close(); err != nil {
		os.Remove(tmpSha.Name())
		return fmt.Errorf("close tmp sha: %w", err)
	}
	if err := os.Rename(tmpSha.Name(), shaPath); err != nil {
		os.Remove(tmpSha.Name())
		return fmt.Errorf("rename sha: %w", err)
	}

	fmt.Printf("Built %s (%s)\n", finalPath, humanSize(statSize(finalPath)))
	fmt.Printf("  sha256: %s\n", sha)

	// Step 4: refresh index.json.
	return reindexAll(outDir, false)
}

// reindexAll scans every tar.gz in outDir, extracts manifest.json + meta.yaml
// from each, and writes a unified dist/index.json with one entry per
// (agent, source) that aggregates all versions.
func reindexAll(outDir string, dryRun bool) error {
	index, err := buildIndexFromDir(outDir)
	if err != nil {
		return err
	}
	out := filepath.Join(outDir, "index.json")
	if dryRun {
		fmt.Printf("(dry-run) would write %s with %d agent(s)\n", out, len(index.Agents))
		return nil
	}
	data, err := jsonMarshalIndent(index)
	if err != nil {
		return err
	}
	tmp := out + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmp, out); err != nil {
		return err
	}
	fmt.Printf("Wrote %s (%d agent(s))\n", out, len(index.Agents))
	return nil
}

// addFileToTar writes a single regular file to the archive.
func addFileToTar(tw *tar.Writer, srcPath, nameInTar string) error {
	f, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	// Preserve the file's mode bits. The previous version hardcoded
	// Mode: 0644 here, which silently stripped the +x bit from shell
	// scripts (install.sh / uninstall.sh / render-config.sh) and from
	// shipped binaries (bin/<name>). The kernel would then refuse
	// execve() at install time with EACCES — the install would fail
	// with ``fork/exec .../install.sh: permission denied`` even though
	// the script content was fine. The consumer also runs ``chmod 0755``
	// at exec time as defense in depth (cmd/agentpkg/internal/cli/install.go
	// ``execScript``); preserving the mode here keeps the tarball honest
	// from the start and lets ``tar -tvf`` show meaningful modes.
	mode := fi.Mode().Perm()
	hdr := &tar.Header{Name: nameInTar, Mode: int64(mode), Size: fi.Size(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// addSymlinkToTar writes a symbolic link to the archive, preserving the link
// target verbatim instead of dereferencing to the target's content.
//
// ``filepath.Walk`` reports symlinks via the same FileInfo channel as regular
// files (with ``Mode()&os.ModeSymlink`` set), so the only place to detect
// them is here in the walk callback. ``addFileToTar``'s ``os.Open`` follows
// symlinks — fine for regular files, but for a symlink it transparently
// reads the TARGET file's content and writes it under a regular-file tar
// header, which destroys the link at extract time. On install, the
// launcher's ``import.meta.url`` then fails to resolve ``./dist/entry.js``
// because the launcher is no longer the npm-style symlink that lives next
// to the package's ``lib/`` directory.
//
// We preserve the symlink as ``tar.TypeSymlink`` with the original
// ``Linkname`` (relative path). On extract, the consumer's ``tar -xzf``
// recreates the link, and the launcher's ``import.meta.url`` resolves into
// the package directory as the original npm layout intended.
func addSymlinkToTar(tw *tar.Writer, srcPath, nameInTar string) error {
	target, err := os.Readlink(srcPath)
	if err != nil {
		return fmt.Errorf("readlink %s: %w", srcPath, err)
	}
	hdr := &tar.Header{
		Name:     nameInTar,
		Linkname: target,
		Mode:     0o777,
		Typeflag: tar.TypeSymlink,
	}
	return tw.WriteHeader(hdr)
}

// addDirToTar walks a directory and adds all regular files to the archive,
// preserving relative paths inside the version dir.
func addDirToTar(tw *tar.Writer, baseDir, prefix string) error {
	return filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
		}
		// Handle symlinks BEFORE delegating to addFileToTar — the latter
		// dereferences via os.Open and would otherwise emit a regular-file
		// header carrying the target's content (see addSymlinkToTar's
		// doc comment for the failure mode this prevents).
		if info.Mode()&os.ModeSymlink != 0 {
			rel, relErr := filepath.Rel(baseDir, path)
			if relErr != nil {
				return relErr
			}
			nameInTar := rel
			if prefix != "" {
				nameInTar = prefix + "/" + rel
			}
			return addSymlinkToTar(tw, path, nameInTar)
		}
		rel, err := filepath.Rel(baseDir, path)
		if err != nil {
			return err
		}
		// Skip buildIndexFromDir's expected manifest.json / meta.yaml — those
		// are added at the tarball root, not nested under the version dir.
		if rel == "manifest.json" || rel == "meta.yaml" {
			return nil
		}
		// Preserve rel verbatim. The earlier ``Strip "upstream/0.1.0/"``
		// logic dropped the first path component unconditionally, which
		// collapsed ``payload/bin/opencode`` → ``bin/opencode`` and
		// produced a tarball whose layout did not match the manifest's
		// ``checksums["payload/bin/opencode"]`` keys. install.sh then
		// exited 30 with "manifest references 'payload/bin/opencode'
		// but it is not present". The baseDir is already the version
		// directory (see buildOne's payloadDir), so ``rel`` IS the path
		// the manifest expects inside the tarball.
		nameInTar := rel
		if prefix != "" {
			nameInTar = prefix + "/" + rel
		}
		return addFileToTar(tw, path, nameInTar)
	})
}

// statSize returns file size or 0 on error.
func statSize(path string) int64 {
	fi, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return fi.Size()
}

// humanSize formats a byte count as e.g. "12.3 MB".
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
