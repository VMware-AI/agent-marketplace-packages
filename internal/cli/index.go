package cli

import (
	"encoding/json"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/apitypes"
	"github.com/spf13/cobra"
)

// NewIndexCmd creates `agentpkg index`.
func NewIndexCmd(cfgPath, credsPath *string) *cobra.Command {
	var (
		asJSON  bool
		filter  string
		channel string
		source  string
	)
	c := &cobra.Command{
		Use:   "index [--json] [--filter NAME] [--channel stable] [--source upstream]",
		Short: "List all agent packages available on the configured marketplace",
		RunE: func(cmd *cobra.Command, _ []string) error {
			client, err := NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			var resp apitypes.IndexStripped
			if err := client.do("/api/v1/index", &resp); err != nil {
				return err
			}
			filtered := filterAgents(resp.Agents, filter, channel, source)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(filtered)
			}
			renderAgentsTable(cmd.OutOrStdout(), filtered)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	c.Flags().StringVar(&filter, "filter", "", "filter by agent name (substring)")
	c.Flags().StringVar(&channel, "channel", "", "filter by channel (stable/beta/dev)")
	c.Flags().StringVar(&source, "source", "", "filter by source (upstream/ours)")
	return c
}

// filterAgents applies the optional --filter / --channel / --source flags.
// All filters are AND-ed; empty filter = pass.
func filterAgents(agents []apitypes.AgentStripped, nameSub, channel, source string) []apitypes.AgentStripped {
	out := agents[:0:0]
	for _, a := range agents {
		if nameSub != "" && !strings.Contains(strings.ToLower(a.Name), strings.ToLower(nameSub)) {
			continue
		}
		if channel != "" || source != "" {
			ok := false
			for _, v := range a.Versions {
				if channel != "" && v.Channel != channel {
					continue
				}
				if source != "" && v.Source != source {
					continue
				}
				ok = true
				break
			}
			if !ok {
				continue
			}
		}
		out = append(out, a)
	}
	return out
}