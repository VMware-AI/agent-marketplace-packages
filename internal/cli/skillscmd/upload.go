package skillscmd

import (
	"bytes"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsUploadCmd creates `agentpkg skills upload`.
//
// Uploads a locally-built skill zip to the marketplace via POST
// /api/v1/skills. The zip filename must match
// <name>-<source>-<version>.zip — the server uses the filename as the
// authoritative identity (not the SKILL.md frontmatter). Re-uploading
// the same (name, source, version) returns 409 (npm-style immutability).
//
// `--channel` controls which dist-tag the new version lands in
// (default: stable). `--source` is read from the zip filename and only
// validated against the CLI flag — if they disagree, the upload is
// rejected client-side rather than silently mutating identity.
func NewSkillsUploadCmd() *cobra.Command {
	var (
		channel string
		dryRun  bool
	)
	c := &cobra.Command{
		Use:   "upload <zip>",
		Short: "Upload a skill zip to the marketplace",
		Long: `upload POSTs a multipart file to /api/v1/skills.

The zip filename MUST be <name>-<source>-<version>.zip — the server uses
the filename (not the SKILL.md frontmatter) as the authoritative
identity for the (name, source, version) tuple. Re-uploading the same
triple returns 409 (immutable; bump the version to publish a fix).

Use --channel to pick which dist-tag the new version lands in.
Default: stable.`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			zipPath := args[0]
			if _, err := os.Stat(zipPath); err != nil {
				return fmt.Errorf("cannot read zip: %w", err)
			}
			zipName := filepath.Base(zipPath)
			name, source, version, err := parseSkillZipName(zipName)
			if err != nil {
				return err
			}
			if !skills.IsValidSource(source) {
				return fmt.Errorf("source in filename %q is not allowed (want: community, internal)", source)
			}
			if channel == "" {
				channel = "stable"
			}
			if !skills.IsValidChannel(channel) {
				return fmt.Errorf("channel %q is not allowed (want: stable, beta, edge, internal)", channel)
			}
			if dryRun {
				fmt.Fprintf(cmd.OutOrStdout(),
					"(dry-run) would upload %s as %s/%s/%s (channel=%s)\n",
					zipPath, source, name, version, channel)
				return nil
			}
			client, err := cli.NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			status, body, err := uploadSkillZip(client, zipPath, channel)
			if err != nil {
				return err
			}
			if status >= 400 {
				return fmt.Errorf("upload failed (HTTP %d): %s", status, body)
			}
			fmt.Fprintf(cmd.OutOrStdout(),
				"Uploaded %s/%s/%s (channel=%s, %s)\n",
				source, name, version, channel, summarizeUpload(body))
			return nil
		},
	}
	c.Flags().StringVar(&channel, "channel", "stable", "channel tag (stable|beta|edge|internal)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be uploaded, don't make the request")
	return c
}

// uploadSkillZip performs the multipart POST. Returns (statusCode, body, error).
// 0 status + nil error means the HTTP call itself failed.
//
// Mirrors the multipart upload pattern in internal/cli (if it exists for
// agents), otherwise uses stdlib mime/multipart.
func uploadSkillZip(c *cli.Client, zipPath, channel string) (int, string, error) {
	f, err := os.Open(zipPath)
	if err != nil {
		return 0, "", fmt.Errorf("open zip: %w", err)
	}
	defer f.Close()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile("file", filepath.Base(zipPath))
	if err != nil {
		return 0, "", fmt.Errorf("create form file: %w", err)
	}
	if _, err := io.Copy(fw, f); err != nil {
		return 0, "", fmt.Errorf("copy zip body: %w", err)
	}
	if err := mw.Close(); err != nil {
		return 0, "", fmt.Errorf("close multipart: %w", err)
	}
	url := c.BaseURL + "/api/v1/skills?channel=" + channel
	req, err := http.NewRequest(http.MethodPost, url, &buf)
	if err != nil {
		return 0, "", err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", fmt.Errorf("read response: %w", err)
	}
	return resp.StatusCode, string(body), nil
}

// summarizeUpload returns a short human-readable description of the new
// skill version JSON returned by the server (e.g. "1.2 KB sha256:abcd...").
// Best-effort: errors are swallowed so a slightly malformed response
// doesn't mask a successful upload.
func summarizeUpload(body string) string {
	// Cheap scan — avoid importing encoding/json in this hot path. The
	// fields we need are top-level JSON strings/numbers; anything else
	// just degrades to "<size> bytes uploaded".
	if idx := indexOfJSONField(body, "size_bytes"); idx >= 0 {
		end := skipJSONNumber(body, idx)
		return body[idx:end] + " bytes uploaded"
	}
	return "<unknown size>"
}

func indexOfJSONField(s, field string) int {
	needle := `"` + field + `":`
	for i := 0; i+len(needle) <= len(s); i++ {
		if s[i:i+len(needle)] == needle {
			return i + len(needle)
		}
	}
	return -1
}

func skipJSONNumber(s string, start int) int {
	i := start
	for i < len(s) && (s[i] >= '0' && s[i] <= '9') {
		i++
	}
	return i
}
