package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/spf13/cobra"
)

// NewDownloadCmd creates `agentpkg download`.
func NewDownloadCmd(cfgPath, credsPath *string) *cobra.Command {
	var (
		source  string
		channel string
		out     string
	)
	c := &cobra.Command{
		Use:   "download <agent> <source> <version> [-o path]",
		Short: "Download a tarball + sha256 sidecar, verify, write to disk",
		Args:  cobra.ExactArgs(3),
		RunE: func(cmd *cobra.Command, args []string) error {
			name, sourceArg, version := args[0], args[1], args[2]
			_ = source // declared for API symmetry; passed as positional
			_ = channel
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			// Resolve the tarball filename + sha256 from the manifest.
			tarName, expectedSHA, err := resolveTarball(client, name, sourceArg, channel, version)
			if err != nil {
				return err
			}
			if out == "" {
				out = tarName
			}
			// Stream tarball
			if err := downloadAndVerify(client, name, sourceArg, channel, version, tarName, expectedSHA, out); err != nil {
				return err
			}
			fmt.Printf("Downloaded %s -> %s\n", tarName, out)
			return nil
		},
	}
	c.Flags().StringVar(&source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVarP(&out, "output", "o", "", "output path (default: <name>-<source>-<version>.tar.gz)")
	return c
}

// resolveTarball looks up one version's tarball filename + expected sha256.
//
// Some agents (notably openclaw and hermes-agent) ship with a placeholder
// "sha256:TBD" in the embedded manifest because the real package payload is
// fetched from npm/PyPI at install time. When we see that placeholder we
// fall back to the authoritative .sha256 sidecar served by the server at
// /api/v1/agents/<n>/<s>/<v>/sha256 — that file hashes the actual bytes
// on disk and is always current.
func resolveTarball(c *Client, name, source, channel, version string) (string, string, error) {
	path := fmt.Sprintf("/api/v1/agents/%s/%s/%s/manifest", name, source, version)
	var m apitypes.Manifest
	if err := c.do(path, &m); err != nil {
		return "", "", err
	}
	if m.Tarball == nil {
		return "", "", fmt.Errorf("manifest has no tarball reference")
	}
	sha := m.Tarball.SHA256
	if isTarballSHAPlaceholder(sha) {
		sidecar, err := fetchSidecarSHA(c, name, source, version)
		if err != nil {
			return "", "", fmt.Errorf("manifest.tarball.sha256 is %q and sidecar fetch failed: %w", sha, err)
		}
		sha = "sha256:" + sidecar
	}
	return m.Tarball.Filename, sha, nil
}

// isTarballSHAPlaceholder reports whether the embedded manifest's tarball
// SHA is a known placeholder (empty, "sha256:TBD", or anything that ends
// in ":TBD"). In those cases the actual bytes are still authoritative —
// the server exposes their hash via the /sha256 sidecar endpoint.
func isTarballSHAPlaceholder(s string) bool {
	if s == "" {
		return true
	}
	const tbd = "sha256:TBD"
	if s == tbd {
		return true
	}
	return strings.HasSuffix(s, ":TBD")
}

// fetchSidecarSHA downloads the .sha256 sidecar text and returns the hex
// prefix. Format matches `sha256sum`: "<hex>  <filename>\n".
func fetchSidecarSHA(c *Client, name, source, version string) (string, error) {
	path := fmt.Sprintf("/api/v1/agents/%s/%s/%s/sha256", name, source, version)
	req, err := http.NewRequest(http.MethodGet, c.BaseURL+path, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", fmt.Errorf("download sidecar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d fetching sidecar", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 {
		return "", fmt.Errorf("empty sidecar response")
	}
	return fields[0], nil
}

// downloadAndVerify streams the tarball, computes its sha256 in-flight, and
// rejects if it doesn't match expected. Writes the file atomically via a
// tmp file + rename.
func downloadAndVerify(c *Client, name, source, channel, version, tarName, expectedSHA, out string) error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/agents/%s/%s/%s/tarball",
		c.BaseURL, name, source, version), nil)
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
		return fmt.Errorf("HTTP %d while downloading tarball", resp.StatusCode)
	}
	expectedHex := trimSHA256Prefix(expectedSHA)
	h := sha256.New()
	tmp := out + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("open tmp: %w", err)
	}
	mw := io.MultiWriter(f, h)
	n, err := io.Copy(mw, resp.Body)
	if err != nil {
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
	if out != tarName {
		fmt.Printf("  wrote %d bytes\n", n)
	}
	// Resolve the absolute path so callers can chain `bash ./install.sh`.
	abs, _ := filepath.Abs(out)
	_ = abs
	return nil
}

func trimSHA256Prefix(s string) string {
	const prefix = "sha256:"
	if len(s) >= len(prefix) && s[:len(prefix)] == prefix {
		return s[len(prefix):]
	}
	return s
}
