// Package skillscmd implements the `agentpkg skills <subcommand>` group.
//
// Subcommands are split into two flavors:
//
//   - Author-side (no server auth): init, build, verify, sign. Operate on
//     local files only; never call the marketplace-api.
//
//   - Repository-side (Basic Auth, mirrors agent index/show/download):
//     list, show, download, upload, delete. Talk to marketplace-api.
//
// The two halves share helpers.go (zip filename parser, json output, etc.)
// but live in distinct files for clear separation. The split mirrors the
// agent `packagecmd` (author-side) and `cli` (consumer-side) packages —
// but unlike those, skillscmd keeps both sides in one package because the
// author + repo surface is small and sharing `helpers.go` reduces drift.
package skillscmd

import (
	"github.com/spf13/cobra"
)

// NewSkillsCmd returns the `agentpkg skills` parent group with all
// subcommands attached. Pattern matches `newPackageCmd` in
// cmd/agentpkg/main.go — defined here in the package so main.go stays
// thin and all skills subcommand wiring is colocated.
//
// cfg/creds are the persistent --config/--credentials paths from the
// root command. We pass them to SetGlobals once so each subcommand can
// read cfgPath/credsPath as package-level variables (matches the
// pattern used in internal/cli). Author-side commands
// (init/verify/build/sign) and install-side (uninstall/list-installed)
// don't talk to the server so they don't need either path.
func NewSkillsCmd(cfg, creds *string) *cobra.Command {
	SetGlobals(cfg, creds)
	c := &cobra.Command{
		Use:   "skills",
		Short: "Skill registry commands (author + repository)",
		Long: `skills contains both author-side tooling (init / build / verify / sign)
and repository-side commands (list / show / download / upload / delete)
for the skills registry — a parallel layout to the agent marketplace
that ships ZIP packages with a SKILL.md manifest.

Author-side subcommands operate on local files only; repository-side
subcommands talk to the configured marketplace-api (see agentpkg login).`,
	}
	c.AddCommand(NewSkillsInitCmd())
	c.AddCommand(NewSkillsVerifyCmd())
	c.AddCommand(NewSkillsBuildCmd())
	c.AddCommand(NewSkillsSignCmd())
	c.AddCommand(NewSkillsListCmd())
	c.AddCommand(NewSkillsShowCmd())
	c.AddCommand(NewSkillsDownloadCmd())
	c.AddCommand(NewSkillsUploadCmd())
	c.AddCommand(NewSkillsDeleteCmd())
	c.AddCommand(NewSkillsInstallCmd())
	c.AddCommand(NewSkillsUninstallCmd())
	c.AddCommand(NewSkillsListInstalledCmd())
	return c
}
