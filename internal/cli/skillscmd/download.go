package skillscmd

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsDownloadCmd creates `agentpkg skills download`.
//
// Streams the skill zip to disk, computes sha256 in-flight, and verifies
// against the sidecar. Defaults to the highest semver in --channel
// (stable by default). Mirrors `agentpkg download` byte-for-byte but
// targets the skills routes.
func NewSkillsDownloadCmd() *cobra.Command {
	var (
		source  string
		channel string
		version string
		out     string
	)
	c := &cobra.Command{
		Use:   "download <name> [--source community|internal] [--channel stable|beta|edge|internal] [--version X.Y.Z] [-o path.zip]",
		Short: "Download a skill zip + sha256 sidecar, verify, write to disk",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if source == "" {
				source = "community"
			}
			if !skills.IsValidSource(source) {
				return fmt.Errorf("--source %q is invalid (want: community, internal)", source)
			}
			if channel == "" {
				channel = "stable"
			}
			client, err := cli.NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			v := version
			if v == "" {
				picked, err := pickLatestSkillVersion(client, name, source, channel)
				if err != nil {
					return fmt.Errorf("resolve latest version: %w", err)
				}
				v = picked
				fmt.Fprintf(cmd.OutOrStdout(), "Resolved latest %s: %s\n", name, v)
			}
			// Resolve the zip filename + expected sha256 from the server.
			zipName, expectedSHA, err := resolveSkillBundle(client, source, name, v)
			if err != nil {
				return err
			}
			if out == "" {
				out = zipName
			}
			// Stream zip + verify in-flight.
			if err := downloadSkillAndVerify(client, source, name, v, zipName, expectedSHA, out); err != nil {
				return err
			}
			// Sidecar — always next to the local zip so `sha256sum -c`
			// works out of the box. The server returns the canonical
			// filename inside the sidecar body.
			sidecarOut := out + ".sha256"
			if err := downloadSkillSidecar(client, source, name, v, sidecarOut); err != nil {
				return fmt.Errorf("download sidecar: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Downloaded %s -> %s (+ sidecar)\n", zipName, out)
			return nil
		},
	}
	c.Flags().StringVar(&source, "source", "community", "source tree (community | internal)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | edge | internal)")
	c.Flags().StringVar(&version, "version", "", "specific version (default: latest in --channel)")
	c.Flags().StringVarP(&out, "output", "o", "", "output path (default: <name>-<source>-<version>.zip)")
	return c
}

// pickLatestSkillVersion fetches /skills/{source}/{name}, filters to
// the requested channel, and returns the highest semver version string.
// Mirrors pickLatestStable in internal/cli/download.go but for skills.
func pickLatestSkillVersion(c *cli.Client, name, source, channel string) (string, error) {
	path := fmt.Sprintf("/api/v1/skills/%s/%s", source, name)
	var s skills.Skill
	if err := c.Do(path, &s); err != nil {
		return "", err
	}
	v, err := resolveSkillLatestVersion(&s, channel)
	if err != nil {
		return "", err
	}
	return v.Version, nil
}

// resolveSkillBundle fetches the skill version metadata and returns
// (filename, expectedSHA256). The version endpoint always returns the
// full SkillVersion including the zip metadata.
func resolveSkillBundle(c *cli.Client, source, name, version string) (filename, expectedSHA string, err error) {
	path := fmt.Sprintf("/api/v1/skills/%s/%s/%s", source, name, version)
	var sv skills.SkillVersion
	if err := c.Do(path, &sv); err != nil {
		return "", "", err
	}
	if sv.Zip.Filename == "" {
		return "", "", fmt.Errorf("server returned no zip filename for %s/%s/%s", source, name, version)
	}
	return sv.Zip.Filename, sv.Zip.SHA256, nil
}

// downloadSkillAndVerify streams the zip, hashes it in-flight, and
// rejects if the result doesn't match the expected hex. Atomic via
// tmp-file + rename.
func downloadSkillAndVerify(c *cli.Client, source, name, version, zipName, expectedSHA, out string) error {
	url := fmt.Sprintf("%s/api/v1/skills/%s/%s/%s/download", c.BaseURL, source, name, version)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d while downloading zip", resp.StatusCode)
	}
	expectedHex := trimSHA256Prefix(expectedSHA)
	h := sha256.New()
	tmp := out + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	mw := io.MultiWriter(f, h)
	if _, err := io.Copy(mw, resp.Body); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("download body: %w", err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("close tmp: %w", err)
	}
	actualHex := hex.EncodeToString(h.Sum(nil))
	if actualHex != expectedHex {
		_ = os.Remove(tmp)
		return fmt.Errorf("sha256 mismatch: expected %s, got %s", expectedHex, actualHex)
	}
	if err := os.Rename(tmp, out); err != nil {
		return fmt.Errorf("rename: %w", err)
	}
	return nil
}

// downloadSkillSidecar fetches the .sha256 sidecar text and writes it
// next to the zip. The server always returns the canonical filename
// inside the sidecar body.
func downloadSkillSidecar(c *cli.Client, source, name, version, outPath string) error {
	url := fmt.Sprintf("%s/api/v1/skills/%s/%s/%s/sha256", c.BaseURL, source, name, version)
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("fetch sidecar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d fetching sidecar", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read sidecar: %w", err)
	}
	// Defensive sanity: the sidecar body should reference a .zip file.
	if !strings.Contains(string(data), ".zip") {
		return fmt.Errorf("sidecar body does not look like a sha256sum-format line: %q", string(data))
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	return nil
}

// trimSHA256Prefix strips the "sha256:" prefix from a hex digest string
// for in-flight comparison. Local copy from internal/cli/download.go —
// duplicated to avoid exporting from cli (and to keep skillscmd
// independent of the agent download flow's helpers).
func trimSHA256Prefix(s string) string {
	const prefix = "sha256:"
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}

// pathBase is unused but kept here as documentation for the helper
// pattern of stripping a path to its base component. Used by tests
// for the resolver pattern (see list.go:resolveSkillLatestVersion).
var _ = filepath.Base