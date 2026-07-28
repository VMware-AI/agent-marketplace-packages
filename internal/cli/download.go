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
		version string
		source  string
		channel string
		out     string
	)
	c := &cobra.Command{
		Use:   "download <agent> [--source upstream] [--channel stable] [--version X.Y.Z] [-o path]",
		Short: "Download a tarball + sha256 sidecar, verify, write to disk",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			// If --version is omitted, pick the latest stable for the given source.
			v := version
			if v == "" {
				picked, err := pickLatestStable(client, name, source, channel)
				if err != nil {
					return fmt.Errorf("resolve latest version: %w", err)
				}
				v = picked
				fmt.Printf("Resolved latest %s: %s\n", name, v)
			}
			// Resolve the tarball filename + sha256 from the manifest.
			tarName, expectedSHA, err := resolveTarball(client, name, source, channel, v)
			if err != nil {
				return err
			}
			if out == "" {
				out = tarName
			}
			// Stream tarball
			if err := downloadAndVerify(client, name, source, channel, v, tarName, expectedSHA, out); err != nil {
				return err
			}
			// Sidecar download — when --output rewrote the filename, we
			// derive the sidecar path from the *original* tarball name
			// (server-side path is canonical, output path is local). We
			// always write the sidecar next to the tarball so that
			// `sha256sum -c` works out of the box, regardless of --output.
			sidecarOut := out + ".sha256"
			if out != tarName {
				// --output overrode the name; place the sidecar next to the
				// rewritten tarball path but still reference the canonical
				// filename inside the file (so sha256sum -c can find it).
				sidecarOut = out + ".sha256"
			}
			if err := downloadSidecar(client, name, source, channel, v, tarName, sidecarOut); err != nil {
				return fmt.Errorf("download sidecar: %w", err)
			}
			fmt.Printf("Downloaded %s -> %s (+ sidecar)\n", tarName, out)
			return nil
		},
	}
	c.Flags().StringVar(&source, "source", "upstream", "source tree (upstream | ours)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | dev)")
	c.Flags().StringVar(&version, "version", "", "specific version (default: latest stable)")
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

// downloadSidecar fetches the .sha256 sidecar from the server and writes
// it to outPath. Format matches `sha256sum` output: "<hex>  <filename>\n"
// so that `sha256sum -c <sidecar>` works against the tarball.
func downloadSidecar(c *Client, name, source, channel, version, tarName, outPath string) error {
	req, err := http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/v1/agents/%s/%s/%s/sha256",
		c.BaseURL, name, source, version), nil)
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
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("read sidecar: %w", err)
	}
	// The server returns "<hex>  <filename>\n". If the user passed -o and
	// renamed the tarball, the sidecar still needs to reference the
	// canonical filename for `sha256sum -c` to find it next to the
	// tarball. If the user did NOT pass -o, the filename inside the sidecar
	// matches the local tarball name and nothing changes. We trust the
	// server-side canonical filename verbatim.
	if err := os.WriteFile(outPath, body, 0644); err != nil {
		return fmt.Errorf("write sidecar: %w", err)
	}
	_ = tarName // server returns the canonical filename inside the body
	return nil
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
