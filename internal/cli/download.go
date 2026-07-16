package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

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
	c.Flags().StringVar(&out, "o", "", "output path (default: <name>-<source>-<version>.tar.gz)")
	return c
}

// resolveTarball looks up one version's tarball filename + expected sha256.
func resolveTarball(c *Client, name, source, channel, version string) (string, string, error) {
	path := fmt.Sprintf("/api/v1/agents/%s/%s/%s/manifest", name, source, version)
	var m apitypes.Manifest
	if err := c.do(path, &m); err != nil {
		return "", "", err
	}
	if m.Tarball == nil {
		return "", "", fmt.Errorf("manifest has no tarball reference")
	}
	return m.Tarball.Filename, m.Tarball.SHA256, nil
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