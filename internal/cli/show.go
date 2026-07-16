package cli

import (
	"encoding/json"
	"fmt"
	"io"

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
			// Optional version filter
			if version != "" {
				filtered := agent.Versions[:0:0]
				for _, v := range agent.Versions {
					if v.Version == version && (channel == "" || v.Channel == channel) {
						filtered = append(filtered, v)
					}
				}
				agent.Versions = filtered
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