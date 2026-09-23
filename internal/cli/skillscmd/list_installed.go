package skillscmd

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"

	"github.com/spf13/cobra"
)

// NewSkillsListInstalledCmd creates `agentpkg skills list-installed`.
//
// Walks <target-dir>/<name>/state.json for each installed skill and
// prints a table (or `--json`). All operations are local-filesystem —
// this command never talks to the marketplace-api.
func NewSkillsListInstalledCmd() *cobra.Command {
	var (
		targetDir string
		asJSON    bool
	)
	c := &cobra.Command{
		Use:   "list-installed [--target-dir DIR] [--json]",
		Short: "List skills installed locally",
		Long: `list-installed scans <target-dir>/<name>/state.json for every
installed skill and prints a table (or --json).

The "current" version is the one the 'latest' symlink points at.
"Versions" lists every entry in state.json's installed_versions map,
including older releases that haven't been uninstalled yet.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if targetDir == "" {
				targetDir = defaultSkillsStateDir()
			}
			return runListInstalled(cmd, targetDir, asJSON)
		},
	}
	c.Flags().StringVar(&targetDir, "target-dir", "", "state root (default: $HOME/.local/share/agentpkg/skills)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	return c
}

// runListInstalled is split out from the cobra closure so install_test.go
// can invoke it directly.
func runListInstalled(cmd *cobra.Command, targetDir string, asJSON bool) error {
	out := cmd.OutOrStdout()
	entries, err := os.ReadDir(targetDir)
	if err != nil {
		if os.IsNotExist(err) {
			if asJSON {
				fmt.Fprintln(out, "[]")
				return nil
			}
			fmt.Fprintln(out, "(no skills installed)")
			return nil
		}
		return err
	}
	var rows []installedRow
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		st, err := loadSkillState(targetDir, e.Name())
		if err != nil || st == nil {
			// Skip directories that don't have a state.json — likely
			// partial installs or unrelated user data.
			continue
		}
		rows = append(rows, installedRow{
			Name:           st.Name,
			CurrentVersion: st.CurrentVersion,
			Source:         st.Source,
			Channel:        st.Channel,
			TargetDir:      st.TargetDir,
			Versions:       st.InstalledVersions,
		})
	}
	// Sort by name for deterministic output.
	sort.SliceStable(rows, func(i, j int) bool {
		return rows[i].Name < rows[j].Name
	})
	if asJSON {
		enc := json.NewEncoder(out)
		enc.SetIndent("", "  ")
		return enc.Encode(rows)
	}
	if len(rows) == 0 {
		fmt.Fprintln(out, "(no skills installed)")
		return nil
	}
	renderInstalledTable(out, rows)
	return nil
}

// installedRow is the JSON-serializable view of a SkillInstallState.
// Defined at package level so the JSON encoder and table renderer can
// both reference it.
type installedRow struct {
	Name           string                      `json:"name"`
	CurrentVersion string                      `json:"current_version,omitempty"`
	Source         string                      `json:"source,omitempty"`
	Channel        string                      `json:"channel,omitempty"`
	TargetDir      string                      `json:"target_dir,omitempty"`
	Versions       map[string]InstalledVersion `json:"installed_versions"`
}

// renderInstalledTable prints a fixed-width table of installed skills.
// Mirrors the column layout used by `agentpkg skills list` so the two
// read consistently side-by-side.
func renderInstalledTable(w io.Writer, rows []installedRow) {
	fmt.Fprintf(w, "%-24s %-12s %-10s %-10s %-8s\n", "NAME", "CURRENT", "SOURCE", "CHANNEL", "VERSIONS")
	for _, r := range rows {
		fmt.Fprintf(w, "%-24s %-12s %-10s %-10s %-8d\n",
			r.Name, r.CurrentVersion, r.Source, r.Channel, len(r.Versions))
	}
}