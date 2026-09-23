package skillscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsDeleteCmd creates `agentpkg skills delete`.
//
// Two URL shapes map to two modes (mirrors the server's HandleSkillDelete):
//
//   - `agentpkg skills delete <name> --source <s> --version <v>`
//     → DELETE /api/v1/skills/{s}/{name}/{v}
//     Removes one specific (source, name, version) entry.
//
//   - `agentpkg skills delete <name> --source <s>`
//     → DELETE /api/v1/skills/{s}/{name}
//     Removes every version under that (source, name) pair.
//
// --source is required (the registry stores (name, source) as part of the
// identity — deleting across all sources at once would be ambiguous and
// dangerous). --dry-run prints what would happen without making the
// request.
func NewSkillsDeleteCmd() *cobra.Command {
	var (
		source  string
		version string
		dryRun  bool
	)
	c := &cobra.Command{
		Use:   "delete <name> --source community|internal [--version X.Y.Z] [--dry-run]",
		Short: "Delete a skill or one of its versions from the marketplace",
		Long: `delete removes a skill (or one version) from the configured marketplace.

Two modes:
  - with --version: removes that one version
  - without --version: removes every version of <name> under --source

Re-uploading the same (name, source, version) afterward is allowed (the
upload endpoint accepts a name/skill that was previously deleted).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if source == "" {
				return fmt.Errorf("--source is required (community | internal)")
			}
			if !skills.IsValidSource(source) {
				return fmt.Errorf("--source %q is invalid (want: community, internal)", source)
			}
			if version != "" && !looksLikeVersion(version) {
				return fmt.Errorf("--version %q does not look like a version string", version)
			}
			if dryRun {
				if version != "" {
					fmt.Fprintf(cmd.OutOrStdout(),
						"(dry-run) would DELETE /api/v1/skills/%s/%s/%s\n",
						source, name, version)
				} else {
					fmt.Fprintf(cmd.OutOrStdout(),
						"(dry-run) would DELETE /api/v1/skills/%s/%s (all versions)\n",
						source, name)
				}
				return nil
			}
			client, err := cli.NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			path := "/api/v1/skills/" + source + "/" + name
			if version != "" {
				path += "/" + version
			}
			status, body, err := cliDelete(client, path)
			if err != nil {
				return err
			}
			if status >= 400 {
				return fmt.Errorf("delete failed (HTTP %d): %s", status, body)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Deleted %s/%s", source, name)
			if version != "" {
				fmt.Fprintf(cmd.OutOrStdout(), "/%s", version)
			}
			fmt.Fprintln(cmd.OutOrStdout())
			// Best-effort: also surface the server's deleted-list for the
			// all-versions mode ({"deleted": ["file1.zip", ...]}).
			var resp map[string]any
			if err := json.Unmarshal([]byte(body), &resp); err == nil {
				if list, ok := resp["deleted"].([]any); ok {
					for _, v := range list {
						fmt.Fprintf(cmd.OutOrStdout(), "  - %v\n", v)
					}
				}
			}
			return nil
		},
	}
	c.Flags().StringVar(&source, "source", "", "source tree (community | internal) — required")
	c.Flags().StringVar(&version, "version", "", "specific version to delete (omit to delete all)")
	c.Flags().BoolVar(&dryRun, "dry-run", false, "print what would be deleted, don't make the request")
	_ = c.MarkFlagRequired("source")
	return c
}

// cliDelete issues an authenticated DELETE and returns (status, body, err).
// 0 status + nil error means the HTTP call itself failed (network, TLS, etc).
func cliDelete(c *cli.Client, path string) (int, string, error) {
	req, err := http.NewRequest(http.MethodDelete, c.BaseURL+path, nil)
	if err != nil {
		return 0, "", err
	}
	req.SetBasicAuth("agentpkg", c.Password)
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp.StatusCode, "", err
	}
	return resp.StatusCode, string(body), nil
}

// looksLikeVersion is a tiny sanity check — anything matching at least
// MAJOR.MINOR is accepted. The server is the source of truth for full
// semver validation; this filter just catches obvious typos like an
// empty string or "--version foo" before a round-trip.
func looksLikeVersion(v string) bool {
	if v == "" {
		return false
	}
	if !strings.Contains(v, ".") {
		return false
	}
	// Disallow path separators / slashes / spaces; anything else can go
	// to the server which will return 400 for malformed versions.
	for _, ch := range v {
		if ch == '/' || ch == '\\' || ch == ' ' {
			return false
		}
	}
	return true
}