package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/spf13/cobra"
)

// NewShowCmd creates `agentpkg show`.
func NewShowCmd(cfgPath, credsPath *string) *cobra.Command {
	var (
		channel string
		version string
		asJSON  bool
	)
	c := &cobra.Command{
		Use:   "show <agent> [--channel stable] [--version X.Y.Z]",
		Short: "Show details for one agent, including all available versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			path := fmt.Sprintf("/api/v1/agents/%s", name)
			var agent apitypes.Agent
			if err := client.do(path, &agent); err != nil {
				return err
			}
			// F010 (tested 2026-09-30): the old code wrapped the channel
			// check inside `if version != ""`, so `show --channel beta`
			// returned ALL stable versions and silently misled callers.
			// F009 (tested 2026-09-30): the filter used to silently
			// produce an empty list when --version X / --channel X
			// matched nothing, exit 0. Operators got a "Versions:" header
			// with zero entries and assumed the agent had no versions —
			// they actually had a typo in their --version / --channel.
			// We now exit non-zero and print the available versions so
			// the caller can spot the typo immediately.
			if err := filterAgentVersions(&agent, channel, version, name); err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(agent)
			}
			renderAgentDetails(cmd.OutOrStdout(), &agent)
			return nil
		},
	}
	c.Flags().StringVar(&channel, "channel", "", "filter versions by channel")
	c.Flags().StringVar(&version, "version", "", "filter versions by version")
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	return c
}

// filterAgentVersions applies --channel / --version filters to agent.Versions
// in place. Empty filters are no-ops; matching is independent across the
// two filters (see F010).
//
// F009 (tested 2026-09-30): when --channel and/or --version is set and
// no version matches, returns an ExitUsageError listing the available
// versions. The error path was previously silent (just an empty list +
// exit 0), which misled operators who had a typo in their filter.
//
// Exposed as a free function (rather than inlined in RunE) so the
// filter + "did you mean" logic is unit-testable without standing up
// an HTTP test server.
func filterAgentVersions(agent *apitypes.Agent, channel, version, name string) error {
	if channel == "" && version == "" {
		return nil
	}
	filtered := agent.Versions[:0:0]
	for _, v := range agent.Versions {
		if version != "" && v.Version != version {
			continue
		}
		if channel != "" && v.Channel != channel {
			continue
		}
		filtered = append(filtered, v)
	}
	if len(filtered) == 0 {
		// Build the "did you mean" list so the operator can spot
		// the typo. Surface every version (regardless of channel)
		// when --channel / --version matched nothing — the agent
		// exists, the filter just doesn't match.
		available := make([]string, 0, len(agent.Versions))
		for _, v := range agent.Versions {
			available = append(available, fmt.Sprintf("%s (%s/%s)", v.Version, v.Source, v.Channel))
		}
		return ExitErrorf(ExitUsageError,
			"no versions match --channel=%q --version=%q for %s; available: %s",
			channel, version, name, strings.Join(available, ", "))
	}
	agent.Versions = filtered
	return nil
}

// renderAgentDetails prints one agent's metadata and a list of versions.
func renderAgentDetails(w io.Writer, a *apitypes.Agent) {
	fmt.Fprintf(w, "%s  (%s)\n", a.DisplayName, a.Name)
	fmt.Fprintf(w, "  Category: %s\n", a.Category)
	fmt.Fprintf(w, "  Description: %s\n", a.Description)
	fmt.Fprintf(w, "  Tags: %v\n", a.Tags)
	fmt.Fprintln(w, "  Versions:")
	for _, v := range a.Versions {
		fmt.Fprintf(w, "    - %s (%s/%s)  tarball=%s  size=%d  sha256=%s\n",
			v.Version, v.Source, v.Channel,
			v.Tarball.Filename, v.Tarball.SizeBytes, v.Tarball.SHA256)
	}
}
