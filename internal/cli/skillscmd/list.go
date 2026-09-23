package skillscmd

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsListCmd creates `agentpkg skills list`.
//
// Mirrors `agentpkg index` (for agents) but operates on the skills
// collection. Supports per-source filter (`--source`) and per-channel
// filter (`--channel`); default is `stable` channel, all sources.
//
// Output is a table by default; `--json` emits the raw
// `IndexStripped` from the server so downstream tooling can pipe into
// jq / yq / etc.
func NewSkillsListCmd() *cobra.Command {
	var (
		asJSON   bool
		filter   string
		channel  string
		source   string
		allVers  bool
	)
	c := &cobra.Command{
		Use:   "list [--source community|internal] [--channel stable|beta|edge|internal] [--filter NAME] [--all-versions] [--json]",
		Short: "List all skills available on the configured marketplace",
		Long: `list fetches /api/v1/skills and prints a table (or JSON with --json).
The server-side default is channel=stable; pass --channel to override.
--all-versions requests every version of each skill (the server still
applies the channel filter if --channel is set).`,
		RunE: func(cmd *cobra.Command, args []string) error {
			client, err := cli.NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			path := "/api/v1/skills"
			q := make([]string, 0, 2)
			if channel != "" {
				q = append(q, "channel="+channel)
			}
			if source != "" {
				q = append(q, "source="+source)
			}
			if len(q) > 0 {
				path += "?" + strings.Join(q, "&")
			}
			var resp skills.IndexStripped
			if err := client.Do(path, &resp); err != nil {
				return err
			}
			filtered := filterSkills(resp.Skills, filter)
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(filtered)
			}
			renderSkillsTable(cmd.OutOrStdout(), filtered)
			return nil
		},
	}
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of a table")
	c.Flags().StringVar(&filter, "filter", "", "filter by skill name (substring)")
	c.Flags().StringVar(&channel, "channel", "", "filter by channel (stable|beta|edge|internal; default: stable)")
	c.Flags().StringVar(&source, "source", "", "filter by source (community|internal)")
	c.Flags().BoolVar(&allVers, "all-versions", false, "request every version per skill (default: server's channel-projection only)")
	return c
}

// filterSkills applies the local --filter (name substring) on top of
// whatever the server already returned. Server-side ?source / ?channel
// filtering happens before the response is parsed, so by the time we
// get here the slice is already narrow.
func filterSkills(skills []skills.SkillStripped, nameSub string) []skills.SkillStripped {
	if nameSub == "" {
		return skills
	}
	out := skills[:0:0]
	for _, s := range skills {
		if strings.Contains(strings.ToLower(s.Name), strings.ToLower(nameSub)) {
			out = append(out, s)
		}
	}
	return out
}

// resolveSkillLatestVersion walks a skill's version list and returns the
// highest-semver version in the requested channel. Used by show /
// download when --version is empty.
//
// Returns ("", fmt.Errorf) when no matching version exists.
func resolveSkillLatestVersion(s *skills.Skill, channel string) (skills.SkillVersion, error) {
	if channel == "" {
		channel = "stable"
	}
	// The server may have already projected; we trust the input order
	// (caller is expected to have come from /skills/{source}/{name} which
	// is already semver-sorted in descending order). Take the first match.
	for _, v := range s.Versions {
		if v.Channel == channel {
			return v, nil
		}
	}
	return skills.SkillVersion{}, fmt.Errorf("no version in channel %q", channel)
}