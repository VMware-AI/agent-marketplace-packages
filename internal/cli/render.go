package cli

import (
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
)

// renderAgentsTable prints a human-readable table of agent packages.
func renderAgentsTable(w io.Writer, agents []apitypes.AgentStripped) {
	if len(agents) == 0 {
		fmt.Fprintln(w, "(no agents match the filter)")
		return
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "NAME\tDISPLAY\tCATEGORY\tLATEST\tVERSIONS\tDESCRIPTION")
	fmt.Fprintln(tw, "----\t-------\t--------\t------\t--------\t-----------")
	for _, a := range agents {
		latest := latestVersionLabel(a.Versions)
		versions := versionCount(a.Versions)
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			a.Name,
			truncate(a.DisplayName, 18),
			a.Category,
			latest,
			versions,
			truncate(a.Description, 60),
		)
	}
	_ = tw.Flush()
}

// latestVersionLabel returns "X.Y.Z (channel)" for the highest version of
// the agent, falling back to "—" if none.
func latestVersionLabel(versions []apitypes.VersionStripped) string {
	if len(versions) == 0 {
		return "—"
	}
	// Pick the first stable version (or first if no stable).
	var pick *apitypes.VersionStripped
	for i := range versions {
		if versions[i].Channel == "stable" {
			pick = &versions[i]
			break
		}
	}
	if pick == nil {
		pick = &versions[0]
	}
	return fmt.Sprintf("%s (%s)", pick.Version, pick.Channel)
}

// versionCount formats the number of versions for the table.
func versionCount(versions []apitypes.VersionStripped) string {
	if len(versions) == 1 {
		return "1"
	}
	return fmt.Sprintf("%d", len(versions))
}

// truncate returns s shortened to n chars + "…" if it exceeds n.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	return s[:n-1] + "…"
}
