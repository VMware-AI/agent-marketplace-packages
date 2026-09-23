package skillscmd

import (
	"encoding/json"
	"fmt"

	"github.com/VMware-AI/agent-marketplace-packages/internal/cli"
	"github.com/VMware-AI/agent-marketplace-packages/internal/skills"
	"github.com/spf13/cobra"
)

// NewSkillsShowCmd creates `agentpkg skills show`.
//
// Mirrors `agentpkg show` for agents. The default source is `community`;
// the default channel is `stable`; the default version is the highest
// semver in that channel. Pass `--source internal` to inspect official
// skills, `--version` to pin, or `--channel beta` etc. for pre-releases.
func NewSkillsShowCmd() *cobra.Command {
	var (
		source  string
		channel string
		version string
		asJSON  bool
	)
	c := &cobra.Command{
		Use:   "show <name> [--source community|internal] [--channel stable|beta|edge|internal] [--version X.Y.Z]",
		Short: "Show full details for one skill, including all available versions",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := args[0]
			if source == "" {
				source = "community"
			}
			if !skills.IsValidSource(source) {
				return fmt.Errorf("--source %q is invalid (want: community, internal)", source)
			}
			if channel == "" {
				channel = "stable"
			}
			client, err := cli.NewClient(*cfgPath, *credsPath)
			if err != nil {
				return err
			}
			// If --version is given, fetch that specific version directly
			// (no need to walk the all-versions response and filter).
			if version != "" {
				path := fmt.Sprintf("/api/v1/skills/%s/%s/%s", source, name, version)
				var sv skills.SkillVersion
				if err := client.Do(path, &sv); err != nil {
					return err
				}
				if asJSON {
					enc := json.NewEncoder(cmd.OutOrStdout())
					enc.SetIndent("", "  ")
					return enc.Encode(sv)
				}
				renderSkillVersionDetails(cmd.OutOrStdout(), &sv)
				return nil
			}
			// No --version: fetch all versions of this skill, then resolve
			// the latest in the requested channel.
			path := fmt.Sprintf("/api/v1/skills/%s/%s", source, name)
			var s skills.Skill
			if err := client.Do(path, &s); err != nil {
				return err
			}
			// If channel filter is set, project the skill's versions to
			// only that channel and pick the highest semver.
			if channel != "" {
				picked, err := resolveSkillLatestVersion(&s, channel)
				if err != nil {
					return err
				}
				// Render as a single-version Skill so the output shape
				// matches the --version form.
				s.Versions = []skills.SkillVersion{picked}
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				enc.SetIndent("", "  ")
				return enc.Encode(s)
			}
			renderSkillDetails(cmd.OutOrStdout(), &s)
			return nil
		},
	}
	c.Flags().StringVar(&source, "source", "community", "source tree (community | internal)")
	c.Flags().StringVar(&channel, "channel", "stable", "channel (stable | beta | edge | internal)")
	c.Flags().StringVar(&version, "version", "", "specific version (default: latest in --channel)")
	c.Flags().BoolVar(&asJSON, "json", false, "emit JSON instead of human-readable text")
	return c
}