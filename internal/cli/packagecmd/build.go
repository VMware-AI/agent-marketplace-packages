package packagecmd

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

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
		Use:   "build <path> --version X.Y.Z [--source upstream] [--channel stable] [--out dist]",
		Short: "Package one version of an agent into dist/<...>.tar.gz + .sha256 and refresh index.json",
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
			if outDir == "" {
				outDir = "dist"
			}
			agentDir := args[0]
			return buildOne(agentDir, source, channel, version, outDir, dryRun)
		},
	}
	c.Flags().StringVar(&version, "version", "", "semver version segment (required)")
	c.Flags().StringVar(&source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | dev | internal)")
	c.Flags().StringVar(&outDir, "out", "dist", "output directory")
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
	name := filepath.Base(agentDir)
	payloadDir := filepath.Join(agentDir, source, version)
	mf := filepath.Join(payloadDir, "manifest.json")
	if _, err := os.Stat(mf); err != nil {
		return fmt.Errorf("missing manifest.json at %s", mf)
	}

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
	hdr := &tar.Header{Name: nameInTar, Mode: 0644, Size: fi.Size(), Typeflag: tar.TypeReg}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = io.Copy(tw, f)
	return err
}

// addDirToTar walks a directory and adds all regular files to the archive,
// preserving relative paths.
func addDirToTar(tw *tar.Writer, baseDir, prefix string) error {
	return filepath.Walk(baseDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			return nil
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
		nameInTar := rel
		if prefix != "" {
			nameInTar = prefix + "/" + rel
		}
		// Strip "upstream/0.1.0/" prefix — the tarball root IS the version dir.
		parts := strings.SplitN(rel, string(os.PathSeparator), 2)
		if len(parts) == 2 {
			nameInTar = parts[1]
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
